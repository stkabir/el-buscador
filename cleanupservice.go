package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	"el-buscador/internal/actions"
	"el-buscador/internal/devtrash"
	"el-buscador/internal/disk"
	"el-buscador/internal/rules"
)

const (
	cleanRunning = "running"
	cleanDone    = "done"

	maxCleanOutput = 16 * 1024
)

// CleanPlanItem es lo que se va a hacer con un elemento. Code no vacío significa que
// no se puede limpiar, y dice por qué.
type CleanPlanItem struct {
	Path   string         `json:"path"`
	Size   int64          `json:"size"`
	Advice rules.Advice   `json:"advice"`
	Synced bool           `json:"synced"`
	Method actions.Method `json:"method"`
	// Command es el comando que se ejecutará, para mostrarlo antes de hacerlo.
	Command string `json:"command"`
	// MissingTool es la herramienta que no está instalada. Por eso va a la papelera.
	MissingTool string       `json:"missingTool"`
	Code        actions.Code `json:"code"`
}

// CleanPlan es lo que el usuario confirma. Total solo suma lo que se puede limpiar.
type CleanPlan struct {
	Items []CleanPlanItem `json:"items"`
	Total int64           `json:"total"`
}

// CleanRequest es un elemento confirmado. Nunca trae el comando: se toma del escaneo,
// para que la interfaz no pueda pedir que se ejecute otra cosa.
type CleanRequest struct {
	Path            string         `json:"path"`
	Method          actions.Method `json:"method"`
	ConfirmedReview bool           `json:"confirmedReview"`
}

// CleanStatus es el estado que el frontend consulta durante una limpieza. Freed es lo
// que el disco ganó de verdad; Trashed sigue ocupando espacio hasta vaciar la papelera.
type CleanStatus struct {
	State    string            `json:"state"`
	Current  string            `json:"current"`
	Total    int               `json:"total"`
	Outcomes []actions.Outcome `json:"outcomes"`
	Output   string            `json:"output"`
	Trashed  int64             `json:"trashed"`
	Freed    int64             `json:"freed"`
	FreedOK  bool              `json:"freedOk"`
}

// CleanupService limpia lo que el usuario confirmó, un trabajo a la vez.
type CleanupService struct {
	scan *ScanService

	mu     sync.Mutex
	status CleanStatus
	cancel context.CancelFunc
	output []byte
}

// guard valida contra el disco actual y el último escaneo. Solo se puede limpiar lo
// que el escaneo encontró.
func (s *CleanupService) guard(ctx context.Context) actions.Guard {
	return actions.Guard{
		Env:      rules.EnvFromOS(),
		Scanned:  s.scan.scannedIsDir,
		Classify: func(path string, isDir bool) (rules.Advice, bool) { return s.scan.classify(ctx, path, isDir) },
	}
}

// Plan valida una selección y dice qué pasaría con cada elemento, sin tocar nada.
func (s *CleanupService) Plan(paths []string) (CleanPlan, error) {
	ctx := context.Background()
	guard := s.guard(ctx)
	plan := CleanPlan{Items: []CleanPlanItem{}}
	for _, path := range actions.CollapseNested(paths) {
		item := CleanPlanItem{Path: path, Method: actions.MethodTrash, Size: s.scan.scannedSize(path)}

		checked, err := guard.Check(path, true)
		item.Advice = checked.Advice
		if err != nil {
			item.Code = actions.CodeOf(err)
			if item.Code == "" {
				item.Code = actions.CodeFailed
			}
			plan.Items = append(plan.Items, item)
			continue
		}

		catalog := rules.New(guard.Env)
		item.Synced = catalog.IsSynced(path)
		if finding, ok := s.scan.finding(path); ok && len(finding.Command) > 0 {
			applyCommand(&item, finding)
		}
		plan.Total += item.Size
		plan.Items = append(plan.Items, item)
	}
	return plan, nil
}

// applyCommand prefiere el comando oficial. Sin la herramienta, el elemento va a la
// papelera, y la pantalla de confirmación lo dice antes de hacerlo.
func applyCommand(item *CleanPlanItem, finding devtrash.Finding) {
	if !actions.ToolInstalled(finding.Command[0]) {
		item.MissingTool = finding.Command[0]
		return
	}
	item.Method = actions.MethodCommand
	item.Command = strings.Join(finding.Command, " ")
}

// Start limpia en segundo plano. Si ya hay una limpieza en curso, no hace nada.
func (s *CleanupService) Start(requests []CleanRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.State == cleanRunning {
		return nil
	}
	if s.scan.isBusy() {
		return errors.New("hay un escaneo en curso")
	}

	work := make([]actions.Request, 0, len(requests))
	for _, request := range requests {
		item := actions.Request{Path: request.Path, Method: request.Method, ConfirmedReview: request.ConfirmedReview}
		if request.Method == actions.MethodCommand {
			if finding, ok := s.scan.finding(request.Path); ok {
				item.Command = finding.Command
			}
		}
		work = append(work, item)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.output = nil
	s.status = CleanStatus{State: cleanRunning, Total: len(work), Outcomes: []actions.Outcome{}}

	home := rules.EnvFromOS().Home
	cleaner := actions.Cleaner{
		Guard:  s.guard(ctx),
		Trash:  actions.MoveToTrash,
		Output: cleanOutput{s},
		Command: func(ctx context.Context, argv []string, output io.Writer) error {
			return actions.RunCommand(ctx, argv, home, output)
		},
	}

	before, beforeErr := disk.System()
	go func() {
		defer cancel()
		cleaner.Run(ctx, work, s.setCurrent, s.addOutcome)

		after, afterErr := disk.System()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.status.State, s.status.Current = cleanDone, ""
		if beforeErr == nil && afterErr == nil {
			s.status.FreedOK = true
			if after.Free > before.Free {
				s.status.Freed = int64(after.Free - before.Free)
			}
		}
	}()
	return nil
}

func (s *CleanupService) setCurrent(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Current = path
}

func (s *CleanupService) addOutcome(outcome actions.Outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Outcomes = append(s.status.Outcomes, outcome)
	s.status.Trashed += outcome.Trashed
}

// cleanOutput guarda la salida de los comandos, solo el final si es muy larga. Es un
// tipo aparte para que Wails no exponga Write a la interfaz.
type cleanOutput struct{ s *CleanupService }

func (w cleanOutput) Write(data []byte) (int, error) {
	s := w.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output = append(s.output, data...)
	if extra := len(s.output) - maxCleanOutput; extra > 0 {
		s.output = s.output[extra:]
	}
	return len(data), nil
}

// Status devuelve el avance actual.
func (s *CleanupService) Status() CleanStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status
	status.Outcomes = append([]actions.Outcome{}, s.status.Outcomes...)
	status.Output = strings.ToValidUTF8(string(s.output), "")
	return status
}

// Cancel detiene la limpieza. Lo que ya llegó a la papelera ahí se queda.
func (s *CleanupService) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.State == cleanRunning {
		s.cancel()
	}
}

// Reveal muestra en el Explorador un elemento del último escaneo. Solo lee.
func (s *CleanupService) Reveal(path string) error {
	if _, known := s.scan.scannedIsDir(path); !known {
		return errNoScan
	}
	return actions.Reveal(path)
}

// OpenRecycleBin abre la papelera. Nunca la vacía.
func (s *CleanupService) OpenRecycleBin() error {
	return actions.OpenRecycleBin()
}
