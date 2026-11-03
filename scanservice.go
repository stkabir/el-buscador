package main

import (
	"context"
	"errors"
	"sync"

	"el-buscador/internal/devtrash"
	"el-buscador/internal/disk"
	"el-buscador/internal/rules"
	"el-buscador/internal/scanner"
)

const (
	stateIdle      = "idle"
	stateScanning  = "scanning"
	stateAnalyzing = "analyzing"
	stateDone      = "done"
	stateCanceled  = "canceled"
	stateFailed    = "failed"
)

// ScanStatus es el estado que el frontend consulta durante un escaneo.
type ScanStatus struct {
	State    string           `json:"state"`
	Error    string           `json:"error"`
	Progress scanner.Progress `json:"progress"`
}

// ScanReport es el resultado de un escaneo con sus recomendaciones por ruta. Una
// ruta sin recomendación queda sin clasificar.
type ScanReport struct {
	scanner.Summary
	Advice map[string]rules.Advice `json:"advice"`
	Dev    devtrash.Report         `json:"dev"`
}

// FolderView es el contenido de una carpeta con sus recomendaciones por ruta.
type FolderView struct {
	scanner.Listing
	Advice map[string]rules.Advice `json:"advice"`
}

// ScanService ejecuta un escaneo a la vez y guarda el último resultado.
type ScanService struct {
	mu       sync.Mutex
	state    string
	err      string
	tracker  *scanner.Tracker
	cancel   context.CancelFunc
	result   *scanner.Result
	catalog  *rules.Catalog
	report   devtrash.Report
	findings map[string]devtrash.Finding
}

func (s *ScanService) isRunning() bool {
	return s.state == stateScanning || s.state == stateAnalyzing
}

// Start escanea el disco del sistema en segundo plano. Si ya hay un escaneo en
// curso, no hace nada.
func (s *ScanService) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isRunning() {
		return nil
	}

	usage, err := disk.System()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	tracker := &scanner.Tracker{}
	catalog := rules.New(rules.EnvFromOS())
	s.state, s.err, s.tracker, s.cancel, s.result = stateScanning, "", tracker, cancel, nil

	go func() {
		defer cancel()
		root := usage.Drive + `\`
		if scanRootOverride != "" {
			root = scanRootOverride
		}
		result, err := scanner.Scan(ctx, root, tracker)

		var report devtrash.Report
		if err == nil {
			s.setState(stateAnalyzing)
			report, err = devtrash.Analyze(ctx, result, catalog)
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case errors.Is(err, context.Canceled):
			s.state = stateCanceled
		case err != nil:
			s.state, s.err = stateFailed, err.Error()
		default:
			findings := make(map[string]devtrash.Finding, len(report.Findings))
			for _, finding := range report.Findings {
				findings[finding.Path] = finding
			}
			s.state, s.result, s.catalog, s.report, s.findings = stateDone, result, catalog, report, findings
		}
	}()
	return nil
}

func (s *ScanService) setState(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

// Cancel detiene el escaneo en curso.
func (s *ScanService) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isRunning() {
		s.cancel()
	}
}

// Status devuelve el estado y el avance actuales.
func (s *ScanService) Status() ScanStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := ScanStatus{State: s.state, Error: s.err}
	if status.State == "" {
		status.State = stateIdle
	}
	if s.tracker != nil {
		status.Progress = s.tracker.Snapshot()
	}
	return status
}

// Summary devuelve el resumen del último escaneo terminado.
func (s *ScanService) Summary() (ScanReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result == nil {
		return ScanReport{}, errNoScan
	}

	summary := s.result.Summary()
	advice := make(map[string]rules.Advice)
	s.addAdvice(advice, summary.TopFiles)
	s.addAdvice(advice, summary.TopDirs)
	return ScanReport{Summary: summary, Advice: advice, Dev: s.report}, nil
}

// Listing devuelve el contenido de una carpeta del último escaneo.
func (s *ScanService) Listing(path string) (FolderView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result == nil {
		return FolderView{}, errNoScan
	}

	listing, err := s.result.Listing(path)
	if err != nil {
		return FolderView{}, err
	}
	advice := make(map[string]rules.Advice)
	s.addAdvice(advice, listing.Dirs)
	s.addAdvice(advice, listing.FileItems)
	return FolderView{Listing: listing, Advice: advice}, nil
}

var errNoScan = errors.New("no hay escaneo terminado")

// addAdvice agrega lo que se sabe de cada ruta. Lo detectado como basura dev manda
// sobre el catálogo general porque ya revisó git y el proyecto.
func (s *ScanService) addAdvice(advice map[string]rules.Advice, entries []scanner.Entry) {
	for _, entry := range entries {
		if finding, ok := s.findings[entry.Path]; ok {
			advice[entry.Path] = finding.Advice
		} else if explained, ok := s.catalog.Explain(entry.Path, entry.IsDir); ok {
			advice[entry.Path] = explained
		}
	}
}

func (s *ScanService) isBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isRunning()
}

// scannedIsDir dice si el último escaneo vio la ruta y si era una carpeta.
func (s *ScanService) scannedIsDir(path string) (isDir, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result == nil {
		return false, false
	}
	entry, ok := s.result.Find(path)
	return entry.IsDir, ok
}

func (s *ScanService) scannedSize(path string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result == nil {
		return 0
	}
	entry, _ := s.result.Find(path)
	return entry.Size
}

func (s *ScanService) finding(path string) (devtrash.Finding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	finding, ok := s.findings[path]
	return finding, ok
}

// classify devuelve el semáforo actual de una ruta. Consulta git de nuevo, así que
// corre sin el candado.
func (s *ScanService) classify(ctx context.Context, path string, isDir bool) (rules.Advice, bool) {
	s.mu.Lock()
	finding, isFinding := s.findings[path]
	catalog := s.catalog
	s.mu.Unlock()
	if catalog == nil {
		return rules.Advice{}, false
	}
	if isFinding {
		return devtrash.Recheck(ctx, finding, catalog)
	}
	return catalog.Explain(path, isDir)
}
