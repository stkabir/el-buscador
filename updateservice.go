package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

const (
	updateRepository = "stkabir/el-buscador"
	updateAsset      = "el-buscador.exe"
	updateChecksums  = "checksums.txt"
	updateDownloads  = "https://github.com"

	// updatePublicKeyHex autentica las actualizaciones. La privada no está en el repo.
	updatePublicKeyHex = "572649428e648483f02ea785318912d1b365567400de4477dda9db0ba5215365"

	updateCheckTimeout = 30 * time.Second
	signatureSize      = 64
	maxSignatureBytes  = 1024

	updateIdle       = "idle"
	updateChecking   = "checking"
	updateAvailable  = "available"
	updateInstalling = "installing"
	updateFailed     = "failed"
)

// version la fija el build de cada release. "dev" apaga las actualizaciones.
var version = "dev"

// Estas variables solo cambian en builds de prueba (ver e2e.go).
var (
	updateAPIOverride string
	updateKeyOverride string
)

// signedProvider exige firma. El proveedor de GitHub de Wails solo trae el checksum,
// que no protege nada si alguien entra a la cuenta: lo publicaría junto con el archivo
// falso. La firma se valida contra la llave incrustada en la app.
type signedProvider struct {
	updater.Provider
	downloads string
	client    *http.Client
}

func (p *signedProvider) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	release, err := p.Provider.Check(ctx, request)
	if err != nil || release == nil {
		return release, err
	}
	if release.Verification == nil || len(release.Verification.Digest) == 0 {
		return nil, errors.New("la versión publicada no trae checksum")
	}
	tag, _ := release.Metadata["github.release.tag"].(string)
	signature, err := p.signature(ctx, tag, release.Artifact.Filename)
	if err != nil {
		return nil, err
	}
	release.Verification.SignatureAlgo = "ed25519"
	release.Verification.Signature = signature
	return release, nil
}

func (p *signedProvider) signature(ctx context.Context, tag, filename string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s/releases/download/%s/%s.sig", p.downloads, updateRepository, tag, filename)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("descargar la firma: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("descargar la firma: respuesta %d", response.StatusCode)
	}
	signature, err := io.ReadAll(io.LimitReader(response.Body, maxSignatureBytes))
	if err != nil {
		return nil, fmt.Errorf("descargar la firma: %w", err)
	}
	if len(signature) != signatureSize {
		return nil, fmt.Errorf("firma de tamaño inválido: %d", len(signature))
	}
	return signature, nil
}

func matchUpdateAsset(_ updater.CheckRequest, assets []github.ReleaseAsset) int {
	for i, asset := range assets {
		if asset.Name == updateAsset {
			return i
		}
	}
	return -1
}

// newUpdateProvider usa un cliente sin límite total: el de 30 s por omisión cortaría
// la descarga en una conexión lenta. El tiempo lo acotan los contextos.
func newUpdateProvider() (updater.Provider, error) {
	client := &http.Client{}
	inner, err := github.New(github.Config{
		Repository:    updateRepository,
		BaseURL:       updateAPIOverride,
		AssetMatcher:  matchUpdateAsset,
		ChecksumAsset: updateChecksums,
		HTTPClient:    client,
	})
	if err != nil {
		return nil, err
	}
	downloads := updateDownloads
	if updateAPIOverride != "" {
		downloads = updateAPIOverride
	}
	return &signedProvider{Provider: inner, downloads: downloads, client: client}, nil
}

func updatePublicKey() ([]byte, error) {
	encoded := updatePublicKeyHex
	if updateKeyOverride != "" {
		encoded = updateKeyOverride
	}
	key, err := hex.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("llave de actualización inválida")
	}
	return key, nil
}

// setupUpdater deja listo el updater. Sin esto, la app simplemente no se actualiza: un
// fallo aquí nunca debe impedir usarla.
func setupUpdater(app *application.App) (bool, error) {
	if version == "dev" {
		return false, nil
	}
	key, err := updatePublicKey()
	if err != nil {
		return false, err
	}
	provider, err := newUpdateProvider()
	if err != nil {
		return false, err
	}
	err = app.Updater.Init(updater.Config{
		CurrentVersion: strings.TrimPrefix(version, "v"),
		Providers:      []updater.Provider{provider},
		PublicKey:      key,
		Window:         updater.WindowNone,
	})
	return err == nil, err
}

// UpdateStatus es lo que el frontend consulta. Error dice que falló, sin detalles: la
// app sigue funcionando con la versión que tiene.
type UpdateStatus struct {
	Version   string `json:"version"`
	State     string `json:"state"`
	Available string `json:"available"`
	Written   int64  `json:"written"`
	Total     int64  `json:"total"`
	Busy      bool   `json:"busy"`
}

// UpdateService revisa si hay versión nueva y la instala solo cuando el usuario lo pide.
type UpdateService struct {
	scan    *ScanService
	cleanup *CleanupService
	dups    *DuplicateService
	enabled bool

	mu        sync.Mutex
	state     string
	available string
	written   int64
	total     int64
}

// setProgress recibe el avance de descarga que emite el updater.
func (s *UpdateService) setProgress(written, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.written, s.total = written, total
}

// busy indica algo que una actualización interrumpiría: reiniciar a media limpieza
// dejaría un lote a medias.
func (s *UpdateService) busy() bool {
	return s.scan.isBusy() || s.cleanup.job.snapshot().State == cleanRunning ||
		s.dups.job.snapshot().State == cleanRunning || s.dups.Status().State == dupSearching
}

// Status devuelve el estado actual.
func (s *UpdateService) Status() UpdateStatus {
	busy := s.busy()
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state
	if state == "" {
		state = updateIdle
	}
	return UpdateStatus{Version: strings.TrimPrefix(version, "v"), State: state, Available: s.available, Written: s.written, Total: s.total, Busy: busy}
}

func (s *UpdateService) setState(state, available string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state, s.available = state, available
}

// Check revisa si hay versión nueva. Solo manda a GitHub la petición de la última
// versión. Sin internet queda igual que antes: no es un error para el usuario.
func (s *UpdateService) Check() UpdateStatus {
	if !s.enabled {
		return s.Status()
	}
	s.setState(updateChecking, "")
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()
	release, err := application.Get().Updater.Check(ctx)
	switch {
	case err != nil:
		application.Get().Logger.Warn("revisar actualizaciones", "error", err)
		s.setState(updateIdle, "")
	case release == nil:
		s.setState(updateIdle, "")
	default:
		s.setState(updateAvailable, release.Version)
	}
	return s.Status()
}

// Install descarga, verifica la firma y reinicia con la versión nueva. El updater
// conserva la versión actual hasta confirmar que la nueva quedó en su lugar.
func (s *UpdateService) Install() error {
	if !s.enabled {
		return errors.New("actualizaciones apagadas")
	}
	if s.busy() {
		return errors.New("hay un trabajo en curso")
	}
	s.mu.Lock()
	if s.state == updateInstalling {
		s.mu.Unlock()
		return nil
	}
	if s.state != updateAvailable && s.state != updateFailed {
		s.mu.Unlock()
		return errors.New("no hay versión nueva")
	}
	available := s.available
	s.state, s.written, s.total = updateInstalling, 0, 0
	s.mu.Unlock()

	go func() {
		ctx := context.Background()
		app := application.Get()
		err := app.Updater.DownloadAndInstall(ctx)
		if err == nil {
			err = app.Updater.Restart(ctx)
		}
		if err != nil {
			// Incluye una firma inválida: el archivo se descarta sin instalarse.
			app.Logger.Error("instalar actualización", "error", err)
			s.setState(updateFailed, available)
		}
	}()
	return nil
}
