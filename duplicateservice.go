package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"el-buscador/internal/actions"
	"el-buscador/internal/devtrash"
	"el-buscador/internal/duplicates"
	"el-buscador/internal/rules"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	dupIdle      = "idle"
	dupSearching = "searching"
	dupDone      = "done"

	// Por qué una copia no se ofrece para borrar.
	lockTracked = "tracked"
	lockKeep    = "keep"
	lockManual  = "manual"

	gitCheckWorkers = 8
)

// DuplicateCopy es una copia dentro de un grupo. Locked no vacío significa que se
// puede conservar, pero no se ofrece para borrar.
type DuplicateCopy struct {
	Path     string `json:"path"`
	Modified int64  `json:"modified"`
	Synced   bool   `json:"synced"`
	Locked   string `json:"locked"`
}

// DuplicateGroup son archivos idénticos. Size es lo que ocupa cada copia en disco, y
// ContentSize el tamaño de su contenido. Reclaimable es lo máximo que se puede liberar
// conservando al menos una copia.
type DuplicateGroup struct {
	Hash        string          `json:"hash"`
	Size        int64           `json:"size"`
	ContentSize int64           `json:"contentSize"`
	Reclaimable int64           `json:"reclaimable"`
	Copies      []DuplicateCopy `json:"copies"`
}

// DuplicateReport es el resultado de una búsqueda.
type DuplicateReport struct {
	Groups      []DuplicateGroup `json:"groups"`
	Reclaimable int64            `json:"reclaimable"`
	Canceled    bool             `json:"canceled"`
	CloudFiles  int64            `json:"cloudFiles"`
	CloudSize   int64            `json:"cloudSize"`
	Unreadable  int64            `json:"unreadable"`
}

// DuplicateStatus es lo que el frontend consulta durante una búsqueda.
type DuplicateStatus struct {
	State    string              `json:"state"`
	Progress duplicates.Progress `json:"progress"`
}

// DuplicateChoice es la decisión sobre un grupo: qué copia queda como garantía y
// cuáles se borran. Las copias que no aparecen se quedan donde están.
type DuplicateChoice struct {
	Keep   string   `json:"keep"`
	Delete []string `json:"delete"`
}

// DuplicateService busca copias sobre el último escaneo y limpia las que el usuario
// elige.
type DuplicateService struct {
	scan *ScanService
	job  cleanJob

	mu         sync.Mutex
	state      string
	tracker    *duplicates.Tracker
	cancel     context.CancelFunc
	report     DuplicateReport
	generation int
	cachePath  string
}

func (s *DuplicateService) hashCachePath() string {
	if s.cachePath != "" {
		return s.cachePath
	}
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "el-buscador")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return filepath.Join(os.TempDir(), "el-buscador-hashes.db")
	}
	return filepath.Join(dir, "hashes.db")
}

// inFolders indica si path está dentro de alguna carpeta. Sin carpetas, vale todo.
func inFolders(path string, folders []string) bool {
	if len(folders) == 0 {
		return true
	}
	lower := strings.ToLower(path)
	return slices.ContainsFunc(folders, func(folder string) bool {
		return strings.HasPrefix(lower, strings.TrimSuffix(strings.ToLower(folder), `\`)+`\`)
	})
}

// ChooseFolder pide una carpeta con el diálogo de Windows. Vacío significa que el
// usuario canceló.
func (s *DuplicateService) ChooseFolder(title string) (string, error) {
	if pick := nextFolderPick(); pick != "" {
		return pick, nil
	}
	return application.Get().Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		SetTitle(title).
		PromptForSingleSelection()
}

// Start busca en segundo plano, en todo el disco o solo entre folders. Si ya hay una
// búsqueda en curso, no hace nada.
func (s *DuplicateService) Start(folders []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == dupSearching {
		return nil
	}
	if s.scan.isBusy() {
		return errors.New("hay un escaneo en curso")
	}
	candidates, generation, err := s.scan.candidates()
	if err != nil {
		return err
	}
	catalog := s.scan.currentCatalog()

	files := make([]duplicates.File, 0, len(candidates))
	for _, candidate := range candidates {
		if path := candidate.Path(); inFolders(path, folders) {
			files = append(files, duplicates.File{
				Path: path, Size: candidate.Size, Allocated: candidate.Allocated,
				Modified: candidate.Modified, CloudOnly: candidate.CloudOnly,
			})
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	tracker := &duplicates.Tracker{}
	s.state, s.tracker, s.cancel, s.generation = dupSearching, tracker, cancel, generation
	s.report = DuplicateReport{Groups: []DuplicateGroup{}}
	cachePath := s.hashCachePath()

	go func() {
		defer cancel()
		cache := duplicates.OpenCache(cachePath)
		defer cache.Close()
		found := duplicates.Find(ctx, files, duplicates.DiskReader{}, cache, tracker)
		report := buildReport(context.Background(), found, catalog)

		s.mu.Lock()
		defer s.mu.Unlock()
		// Un escaneo nuevo pudo empezar mientras tanto: este resultado ya no sirve.
		if s.generation != generation {
			return
		}
		s.state, s.report = dupDone, report
	}()
	return nil
}

// buildReport decide qué copias se ofrecen y cuánto se puede liberar.
func buildReport(ctx context.Context, found duplicates.Result, catalog *rules.Catalog) DuplicateReport {
	report := DuplicateReport{
		Groups: []DuplicateGroup{}, Canceled: found.Canceled,
		CloudFiles: found.CloudFiles, CloudSize: found.CloudSize, Unreadable: found.Unreadable,
	}

	type slot struct {
		group, index int
		path         string
	}
	groups := make([]DuplicateGroup, len(found.Groups))
	var slots []slot
	for i, group := range found.Groups {
		copies := make([]DuplicateCopy, len(group.Files))
		for j, file := range group.Files {
			copies[j] = DuplicateCopy{Path: file.Path, Modified: fileTimeToUnix(file.Modified)}
			slots = append(slots, slot{i, j, file.Path})
		}
		groups[i] = DuplicateGroup{Hash: group.Hash, Size: group.Allocated, ContentSize: group.Size, Copies: copies}
	}

	var wg sync.WaitGroup
	gate := make(chan struct{}, gitCheckWorkers)
	for _, item := range slots {
		wg.Add(1)
		gate <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-gate }()
			target := &groups[item.group].Copies[item.index]
			target.Locked = lockReason(ctx, item.path, catalog)
			target.Synced = catalog != nil && catalog.IsSynced(item.path)
		}()
	}
	wg.Wait()

	for _, group := range groups {
		group.Reclaimable = reclaimable(group)
		if group.Reclaimable > 0 {
			report.Reclaimable += group.Reclaimable
			report.Groups = append(report.Groups, group)
		}
	}
	return report
}

func fileTimeToUnix(fileTime int64) int64 {
	const (
		intervalsPerSecond = 10_000_000
		secondsTo1970      = 11_644_473_600
	)
	return max(fileTime/intervalsPerSecond-secondsTo1970, 0)
}

// lockReason dice por qué una copia no se puede borrar. Lo rastreado por git nunca se
// ofrece: borrarlo rompe el repo aunque exista otra copia afuera.
func lockReason(ctx context.Context, path string, catalog *rules.Catalog) string {
	if catalog != nil {
		if advice, ok := catalog.Explain(path, false); ok {
			if advice.Level == rules.LevelKeep {
				return lockKeep
			}
			if advice.Manual {
				return lockManual
			}
		}
	}
	if devtrash.GitTracksFile(ctx, path) {
		return lockTracked
	}
	return ""
}

// reclaimable siempre deja una copia: si todas se pueden borrar, una se queda.
func reclaimable(group DuplicateGroup) int64 {
	free := 0
	for _, item := range group.Copies {
		if item.Locked == "" {
			free++
		}
	}
	if free == len(group.Copies) {
		free--
	}
	return group.Size * int64(free)
}

// syncGeneration invalida lo calculado si hubo un escaneo nuevo.
func (s *DuplicateService) syncGeneration() {
	if s.state == "" || s.state == dupIdle {
		return
	}
	if s.generation != s.scan.currentGeneration() {
		if s.cancel != nil {
			s.cancel()
		}
		s.state, s.report = dupIdle, DuplicateReport{Groups: []DuplicateGroup{}}
	}
}

// Status devuelve el estado y el avance actuales.
func (s *DuplicateService) Status() DuplicateStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncGeneration()
	status := DuplicateStatus{State: s.state}
	if status.State == "" {
		status.State = dupIdle
	}
	if s.tracker != nil {
		status.Progress = s.tracker.Snapshot()
	}
	return status
}

// Cancel detiene la búsqueda. Lo calculado se conserva y se reporta.
func (s *DuplicateService) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == dupSearching {
		s.cancel()
	}
}

// Report devuelve el resultado de la última búsqueda.
func (s *DuplicateService) Report() DuplicateReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncGeneration()
	if s.report.Groups == nil {
		s.report.Groups = []DuplicateGroup{}
	}
	return s.report
}

// dupTask es lo decidido para una copia. Si err no es nil, se rechaza sin tocarla.
type dupTask struct {
	keep string
	hash string
	size int64
	err  error
}

// planChoices valida las decisiones contra el reporte. Nunca se fía de la interfaz:
// la copia que queda y las que se borran deben ser del mismo grupo, y cada grupo tiene
// una sola copia garantizada. Sin esto, "conservar A y borrar B" más "conservar B y
// borrar A" dejaría sin ninguna.
func planChoices(report DuplicateReport, choices []DuplicateChoice) ([]actions.Request, map[string]dupTask) {
	type member struct {
		group *DuplicateGroup
		item  DuplicateCopy
	}
	members := make(map[string]member)
	for i := range report.Groups {
		group := &report.Groups[i]
		for _, item := range group.Copies {
			members[strings.ToLower(item.Path)] = member{group, item}
		}
	}

	work := []actions.Request{}
	tasks := make(map[string]dupTask)
	guaranteed := make(map[*DuplicateGroup]string)
	for _, choice := range choices {
		keepLower := strings.ToLower(choice.Keep)
		kept, keepKnown := members[keepLower]
		conflict := slices.ContainsFunc(choice.Delete, func(path string) bool { return strings.EqualFold(path, choice.Keep) })
		if keepKnown {
			if previous, decided := guaranteed[kept.group]; decided && previous != keepLower {
				conflict = true
			} else {
				guaranteed[kept.group] = keepLower
			}
		}

		for _, path := range choice.Delete {
			lower := strings.ToLower(path)
			if _, seen := tasks[lower]; seen {
				continue
			}
			task := dupTask{keep: choice.Keep}
			target, known := members[lower]
			switch {
			case !known || !keepKnown || target.group != kept.group:
				task.err = actions.Refuse(actions.CodeNotDuplicate)
			case conflict:
				task.err = actions.Refuse(actions.CodeNoCopyLeft)
			case target.item.Locked != "":
				task.err = actions.Refuse(actions.CodeKeep)
			default:
				task.hash, task.size = kept.group.Hash, kept.group.ContentSize
			}
			tasks[lower] = task
			work = append(work, actions.Request{Path: path, Method: actions.MethodTrash, ConfirmedReview: true})
		}
	}
	return work, tasks
}

// Clean manda a la papelera las copias elegidas, verificando cada una antes.
func (s *DuplicateService) Clean(choices []DuplicateChoice) error {
	s.mu.Lock()
	if s.state != dupDone {
		s.mu.Unlock()
		return errors.New("no hay búsqueda terminada")
	}
	s.syncGeneration()
	report := s.report
	generation := s.generation
	s.mu.Unlock()
	if s.scan.isBusy() || s.scan.currentGeneration() != generation {
		return errNoScan
	}

	work, tasks := planChoices(report, choices)
	ctx, ok := s.job.begin(len(work))
	if !ok {
		return nil
	}

	catalog := s.scan.currentCatalog()
	cleaner := actions.Cleaner{
		Guard: actions.Guard{
			Env: rules.EnvFromOS(),
			Scanned: func(path string) (bool, bool) {
				_, known := tasks[strings.ToLower(path)]
				return false, known
			},
			Classify: func(path string, _ bool) (rules.Advice, bool) { return classifyCopy(ctx, path, catalog) },
		},
		Trash: func(path string, canceled func() bool) (string, error) {
			task := tasks[strings.ToLower(path)]
			if task.err != nil {
				return "", task.err
			}
			if err := duplicates.VerifyCopy(ctx, task.keep, path, task.hash, task.size); err != nil {
				return "", err
			}
			return actions.MoveToTrash(path, canceled)
		},
		Output: cleanOutput{&s.job},
		Room:   binRoom,
	}
	s.job.run(ctx, cleaner, work, s.forgetCleaned)
	return nil
}

// classifyCopy vuelve a clasificar al momento de borrar. Toda copia es Revisar:
// ninguna regla dijo que fuera basura, lo decidió el usuario.
func classifyCopy(ctx context.Context, path string, catalog *rules.Catalog) (rules.Advice, bool) {
	switch lockReason(ctx, path, catalog) {
	case lockKeep, lockTracked:
		return rules.Advice{Level: rules.LevelKeep}, true
	case lockManual:
		return rules.Advice{Level: rules.LevelReview, Manual: true}, true
	}
	return rules.Advice{Level: rules.LevelReview}, true
}

// forgetCleaned quita del reporte lo que ya se fue a la papelera.
func (s *DuplicateService) forgetCleaned(outcomes []actions.Outcome) {
	gone := make(map[string]bool)
	for _, outcome := range outcomes {
		if outcome.Status == actions.StatusDone {
			gone[strings.ToLower(outcome.Path)] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	groups := []DuplicateGroup{}
	var total int64
	for _, group := range s.report.Groups {
		group.Copies = slices.DeleteFunc(slices.Clone(group.Copies), func(item DuplicateCopy) bool {
			return gone[strings.ToLower(item.Path)]
		})
		group.Reclaimable = reclaimable(group)
		if len(group.Copies) > 1 && group.Reclaimable > 0 {
			total += group.Reclaimable
			groups = append(groups, group)
		}
	}
	s.report.Groups, s.report.Reclaimable = groups, total
}

// BinSpace mide la papelera, para avisar antes de confirmar.
func (s *DuplicateService) BinSpace() actions.BinSpace { return binSpaceOf(s.scan.scannedDrive()) }

// CleanStatus devuelve el avance de la limpieza.
func (s *DuplicateService) CleanStatus() CleanStatus { return s.job.snapshot() }

// CancelClean detiene la limpieza. Lo que ya llegó a la papelera ahí se queda.
func (s *DuplicateService) CancelClean() { s.job.stop() }

// Reveal muestra en el Explorador una copia del reporte. Solo lee.
func (s *DuplicateService) Reveal(path string) error {
	s.mu.Lock()
	known := false
	for _, group := range s.report.Groups {
		if slices.ContainsFunc(group.Copies, func(item DuplicateCopy) bool { return strings.EqualFold(item.Path, path) }) {
			known = true
			break
		}
	}
	s.mu.Unlock()
	if !known {
		return errNoScan
	}
	return actions.Reveal(path)
}
