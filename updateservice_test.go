package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

// fakeGitHub imita lo que usa el proveedor: la API de releases y las descargas.
func fakeGitHub(t *testing.T, tag string, files map[string][]byte) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+updateRepository+"/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		assets := []map[string]any{}
		for name, data := range files {
			url := fmt.Sprintf("%s/%s/releases/download/%s/%s", server.URL, updateRepository, tag, name)
			assets = append(assets, map[string]any{"name": name, "size": len(data), "url": url, "browser_download_url": url})
		}
		json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "assets": assets})
	})
	mux.HandleFunc("/"+updateRepository+"/releases/download/"+tag+"/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/"+updateRepository+"/releases/download/"+tag+"/"):]
		data, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func checkAgainst(t *testing.T, server *httptest.Server, current string) (*updater.Release, error) {
	t.Helper()
	updateAPIOverride = server.URL
	t.Cleanup(func() { updateAPIOverride = "" })
	provider, err := newUpdateProvider()
	if err != nil {
		t.Fatal(err)
	}
	return provider.Check(context.Background(), updater.CheckRequest{CurrentVersion: current, Platform: "windows", Arch: "amd64"})
}

func releaseFiles(exe []byte) map[string][]byte {
	digest := sha256.Sum256(exe)
	return map[string][]byte{
		updateAsset:     exe,
		updateChecksums: []byte(hex.EncodeToString(digest[:]) + "  " + updateAsset + "\n"),
	}
}

func TestProviderAddsSignature(t *testing.T) {
	files := releaseFiles([]byte("programa"))
	signature := make([]byte, signatureSize)
	signature[0] = 7
	files[updateAsset+".sig"] = signature

	release, err := checkAgainst(t, fakeGitHub(t, "v1.2.0", files), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "1.2.0" || release.Artifact.Filename != updateAsset {
		t.Fatalf("%+v", release)
	}
	v := release.Verification
	if v == nil || v.SignatureAlgo != "ed25519" || v.Signature[0] != 7 || v.DigestAlgo != "sha256" || len(v.Digest) != sha256.Size {
		t.Fatalf("%+v", v)
	}
}

// Sin firma válida no hay actualización: quitarla no debe bajar la seguridad.
func TestProviderFailsClosed(t *testing.T) {
	valid := make([]byte, signatureSize)
	cases := map[string]map[string][]byte{
		"sin firma": releaseFiles([]byte("programa")),
		"firma corta": func() map[string][]byte {
			f := releaseFiles([]byte("programa"))
			f[updateAsset+".sig"] = valid[:10]
			return f
		}(),
		"firma larga": func() map[string][]byte {
			f := releaseFiles([]byte("programa"))
			f[updateAsset+".sig"] = make([]byte, 65)
			return f
		}(),
		"sin checksums": {updateAsset: []byte("programa"), updateAsset + ".sig": valid},
		"sin programa":  {updateChecksums: []byte("x"), updateAsset + ".sig": valid},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if release, err := checkAgainst(t, fakeGitHub(t, "v1.2.0", files), "1.0.0"); err == nil {
				t.Fatalf("aceptó: %+v", release)
			}
		})
	}
}

func TestProviderIgnoresSameOrOlderVersions(t *testing.T) {
	files := releaseFiles([]byte("programa"))
	files[updateAsset+".sig"] = make([]byte, signatureSize)
	for _, current := range []string{"1.2.0", "1.3.0"} {
		release, err := checkAgainst(t, fakeGitHub(t, "v1.2.0", files), current)
		if err != nil || release != nil {
			t.Fatalf("%s: %+v, %v", current, release, err)
		}
	}
}

func TestEmbeddedPublicKeyIsValid(t *testing.T) {
	updateAPIOverride = ""
	if key, err := updatePublicKey(); err != nil || len(key) != 32 {
		t.Fatalf("la llave incrustada no sirve: %v", err)
	}
}

func TestInstallRefusedWhenBusyOrUnavailable(t *testing.T) {
	scan := &ScanService{state: stateScanning}
	service := &UpdateService{scan: scan, cleanup: &CleanupService{scan: scan}, dups: &DuplicateService{scan: scan}, enabled: true}
	service.setState(updateAvailable, "1.2.0")
	if err := service.Install(); err == nil {
		t.Fatal("no debía instalar a mitad de un escaneo")
	}
	if status := service.Status(); status.State != updateAvailable || !status.Busy {
		t.Fatalf("%+v", status)
	}

	scan.state = stateDone
	service.setState(updateIdle, "")
	if err := service.Install(); err == nil {
		t.Fatal("no hay versión que instalar")
	}
	if err := (&UpdateService{scan: scan, cleanup: service.cleanup, dups: service.dups}).Install(); err == nil {
		t.Fatal("apagado no debe instalar")
	}
}
