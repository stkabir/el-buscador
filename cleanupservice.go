package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
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

// CleanPlan es lo que el usuario confirma. Total solo suma lo que se puede limpiar, y
// TrashTotal lo que de eso iría a la papelera.
type CleanPlan struct {
	TrashTotal int64            `json:"trashTotal"`
	Bin        actions.BinSpace `json:"bin"`
	Items      []CleanPlanItem  `json:"items"`
	Total      int64            `json:"total"`
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
	job  cleanJob
}

// cleanJob lleva una limpieza en curso: avance, salida de comandos, cancelación y
// espacio medido. Es un campo con nombre para que Wails no exponga sus métodos.
type cleanJob struct {
	mu     sync.Mutex
	status CleanStatus
	cancel context.CancelFunc
	output []byte
}

// begin reserva el trabajo. Devuelve false si ya hay una limpieza en curso.
func (j *cleanJob) begin(total int) (context.Context, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.status.State == cleanRunning {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.output = nil
	j.status = CleanStatus{State: cleanRunning, Total: total, Outcomes: []actions.Outcome{}}
	return ctx, true
}

// run ejecuta en segundo plano y mide cuánto ganó el disco. after corre al terminar,
// antes de que el estado pase a terminado.
func (j *cleanJob) run(ctx context.Context, cleaner actions.Cleaner, work []actions.Request, after func([]actions.Outcome)) {
	drive := ""
	if len(work) > 0 {
		drive = filepath.VolumeName(work[0].Path)
	}
	before, beforeErr := usageOf(drive)
	go func() {
		defer j.cancel()
		cleaner.Run(ctx, work, j.setCurrent, j.addOutcome)
		afterUsage, afterErr := usageOf(drive)

		j.mu.Lock()
		outcomes := append([]actions.Outcome{}, j.status.Outcomes...)
		j.mu.Unlock()
		if after != nil {
			after(outcomes)
		}

		j.mu.Lock()
		defer j.mu.Unlock()
		j.status.State, j.status.Current = cleanDone, ""
		if beforeErr == nil && afterErr == nil {
			j.status.FreedOK = true
			if afterUsage.Free > before.Free {
				j.status.Freed = int64(afterUsage.Free - before.Free)
			}
		}
	}()
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

// binRoom dice cuánto cabe en la papelera del disco de path.
func binRoom(path string) (int64, error) {
	if binRoomOverride >= 0 {
		return binRoomOverride, nil
	}
	space, err := actions.RecycleBinSpace(path)
	return space.Room, err
}

// binSpaceOf mide la papelera de un disco para la confirmación. Si falla queda como
// desconocida, y el backend rechaza al ejecutar.
func binSpaceOf(drive string) actions.BinSpace {
	usage, err := usageOf(drive)
	if err != nil {
		return actions.BinSpace{}
	}
	space, err := actions.RecycleBinSpace(usage.Drive + `\`)
	if err != nil {
		return actions.BinSpace{}
	}
	if binRoomOverride >= 0 {
		space.Room = binRoomOverride
	}
	return space
}

// Plan valida una selección y dice qué pasaría con cada elemento, sin tocar nada.
func (s *CleanupService) Plan(paths []string) (CleanPlan, error) {
	ctx := context.Background()
	guard := s.guard(ctx)
	plan := CleanPlan{Items: []CleanPlanItem{}, Bin: binSpaceOf(s.scan.scannedDrive())}
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
		if item.Method == actions.MethodTrash {
			plan.TrashTotal += item.Size
		}
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

	ctx, ok := s.job.begin(len(work))
	if !ok {
		return nil
	}
	home := rules.EnvFromOS().Home
	cleaner := actions.Cleaner{
		Guard:  s.guard(ctx),
		Trash:  actions.MoveToTrash,
		Output: cleanOutput{&s.job},
		Room:   binRoom,
		Command: func(ctx context.Context, argv []string, output io.Writer) error {
			return actions.RunCommand(ctx, argv, home, output)
		},
	}
	s.job.run(ctx, cleaner, work, nil)
	return nil
}

func usageOf(drive string) (disk.Usage, error) {
	if drive == "" {
		return disk.System()
	}
	return disk.Of(drive)
}

func (j *cleanJob) setCurrent(path string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status.Current = path
}

func (j *cleanJob) addOutcome(outcome actions.Outcome) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status.Outcomes = append(j.status.Outcomes, outcome)
	j.status.Trashed += outcome.Trashed
}

// cleanOutput guarda la salida de los comandos, solo el final si es muy larga.
type cleanOutput struct{ j *cleanJob }

func (w cleanOutput) Write(data []byte) (int, error) {
	j := w.j
	j.mu.Lock()
	defer j.mu.Unlock()
	j.output = append(j.output, data...)
	if extra := len(j.output) - maxCleanOutput; extra > 0 {
		j.output = j.output[extra:]
	}
	return len(data), nil
}

func (j *cleanJob) snapshot() CleanStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	status := j.status
	status.Outcomes = append([]actions.Outcome{}, j.status.Outcomes...)
	status.Output = strings.ToValidUTF8(string(j.output), "")
	return status
}

func (j *cleanJob) stop() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.status.State == cleanRunning {
		j.cancel()
	}
}

// Status devuelve el avance actual.
func (s *CleanupService) Status() CleanStatus { return s.job.snapshot() }

// Cancel detiene la limpieza. Lo que ya llegó a la papelera ahí se queda.
func (s *CleanupService) Cancel() { s.job.stop() }

// Reveal muestra en el Explorador un elemento del último escaneo. Solo lee.
func (s *CleanupService) Reveal(path string) error {
	if _, known := s.scan.scannedIsDir(path); !known {
		return errNoScan
	}
	return actions.Reveal(path)
}

// freeUpCheck valida contra el disco actual, y solo lo que el último escaneo vio.
func (s *CleanupService) freeUpCheck(path string) actions.Code {
	if _, known := s.scan.scannedIsDir(path); !known {
		return actions.CodeNotScanned
	}
	catalog := s.scan.currentCatalog()
	if catalog == nil {
		return actions.CodeNotScanned
	}
	err := actions.CheckFreeUp(path, catalog.IsSynced)
	if err == nil {
		return ""
	}
	if code := actions.CodeOf(err); code != "" {
		return code
	}
	return actions.CodeFailed
}

// FreeUpPreview cuenta lo que liberaría path, sin cambiar nada.
func (s *CleanupService) FreeUpPreview(path string) actions.FreeUpResult {
	if code := s.freeUpCheck(path); code != "" {
		return actions.FreeUpResult{Code: code}
	}
	return actions.SurveyFreeUp(context.Background(), path)
}

// FreeUp pide sacar del disco lo que hay en path, dejándolo en la nube. No borra nada.
func (s *CleanupService) FreeUp(path string) actions.FreeUpResult {
	if code := s.freeUpCheck(path); code != "" {
		return actions.FreeUpResult{Code: code}
	}
	return actions.FreeUpSpace(context.Background(), path)
}

// OpenRecycleBin abre la papelera. Nunca la vacía.
func (s *CleanupService) OpenRecycleBin() error {
	return actions.OpenRecycleBin()
}
