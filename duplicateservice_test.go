package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"el-buscador/internal/actions"
	"el-buscador/internal/devtrash"
	"el-buscador/internal/rules"
	"el-buscador/internal/scanner"
)

const dupSize = 1024 * 1024

func dupBytes(fill byte) []byte {
	data := make([]byte, dupSize)
	for i := range data {
		data[i] = fill
	}
	return data
}

func dupFixture(t *testing.T, files map[string][]byte) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// searched escanea root igual que la app y hace la búsqueda real.
func searched(t *testing.T, root string, folders ...string) *DuplicateService {
	t.Helper()
	result, err := scanner.ScanWith(context.Background(), root, nil, scanner.Options{SkipCandidates: devtrash.SkipForDuplicates})
	if err != nil {
		t.Fatal(err)
	}
	scan := &ScanService{state: stateDone, result: result, catalog: rules.New(rules.EnvFromOS()), findings: map[string]devtrash.Finding{}, generation: 1}
	service := &DuplicateService{scan: scan, cachePath: filepath.Join(t.TempDir(), "hashes.db")}
	if err := service.Start(folders); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if service.Status().State == dupDone {
			return service
		}
	}
	t.Fatal("la búsqueda no terminó")
	return nil
}

func TestSearchFindsCopiesAndSkipsGeneratedFolders(t *testing.T) {
	same := dupBytes('a')
	root := dupFixture(t, map[string][]byte{
		"fotos/a.jpg":                   same,
		"respaldo/a.jpg":                same,
		"proyecto/node_modules/x/a.jpg": same,
		"AppData/Local/a.jpg":           same,
		".oculta/a.jpg":                 same,
		"proyecto/dist/a.jpg":           same,
		"chico/uno.txt":                 []byte("chico"),
		"chico/dos.txt":                 []byte("chico"),
	})
	report := searched(t, root).Report()
	if len(report.Groups) != 1 || len(report.Groups[0].Copies) != 2 {
		t.Fatalf("%+v", report.Groups)
	}
	for _, item := range report.Groups[0].Copies {
		if !strings.Contains(item.Path, "fotos") && !strings.Contains(item.Path, "respaldo") {
			t.Fatalf("no debía compararse: %s", item.Path)
		}
	}
	if report.Reclaimable != report.Groups[0].Size {
		t.Fatalf("debe liberar n-1 copias: %d", report.Reclaimable)
	}
}

func TestHardLinksAreNotDuplicates(t *testing.T) {
	root := dupFixture(t, map[string][]byte{"a.bin": dupBytes('a')})
	if err := os.Link(filepath.Join(root, "a.bin"), filepath.Join(root, "b.bin")); err != nil {
		t.Skipf("hard link no disponible: %v", err)
	}
	if report := searched(t, root).Report(); len(report.Groups) != 0 {
		t.Fatalf("%+v", report.Groups)
	}
}

func TestGitTrackedCopyIsNotOfferedForDeletion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git no instalado")
	}
	same := dupBytes('a')
	root := dupFixture(t, map[string][]byte{"repo/assets/logo.png": same, "suelto/logo.png": same})
	repo := filepath.Join(root, "repo")
	for _, args := range [][]string{{"init", "-q"}, {"add", "assets/logo.png"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	report := searched(t, root).Report()
	if len(report.Groups) != 1 {
		t.Fatalf("%+v", report)
	}
	for _, item := range report.Groups[0].Copies {
		tracked := strings.Contains(item.Path, "repo")
		if tracked != (item.Locked == lockTracked) {
			t.Fatalf("%+v", item)
		}
	}
	if report.Reclaimable != report.Groups[0].Size {
		t.Fatalf("Reclaimable = %d", report.Reclaimable)
	}
}

func TestReclaimableAlwaysLeavesOneCopy(t *testing.T) {
	free := DuplicateGroup{Size: 10, Copies: []DuplicateCopy{{}, {}, {}}}
	if got := reclaimable(free); got != 20 {
		t.Fatalf("%d", got)
	}
	locked := DuplicateGroup{Size: 10, Copies: []DuplicateCopy{{Locked: lockTracked}, {}, {}}}
	if got := reclaimable(locked); got != 20 {
		t.Fatalf("%d", got)
	}
	allLocked := DuplicateGroup{Size: 10, Copies: []DuplicateCopy{{Locked: lockKeep}, {Locked: lockTracked}}}
	if got := reclaimable(allLocked); got != 0 {
		t.Fatalf("%d", got)
	}
}

func testReport() DuplicateReport {
	return DuplicateReport{Groups: []DuplicateGroup{
		{Hash: "uno", Size: 10, ContentSize: 10, Copies: []DuplicateCopy{
			{Path: `C:\a\1.bin`}, {Path: `C:\b\1.bin`}, {Path: `C:\c\1.bin`, Locked: lockTracked},
		}},
		{Hash: "dos", Size: 20, ContentSize: 20, Copies: []DuplicateCopy{{Path: `C:\a\2.bin`}, {Path: `C:\b\2.bin`}}},
	}}
}

func planCode(tasks map[string]dupTask, path string) actions.Code {
	task, ok := tasks[strings.ToLower(path)]
	if !ok {
		return "sin tarea"
	}
	return actions.CodeOf(task.err)
}

func TestPlanChoicesValidatesAgainstReport(t *testing.T) {
	cases := []struct {
		name   string
		choice DuplicateChoice
		path   string
		want   actions.Code
	}{
		{"válida", DuplicateChoice{`C:\a\1.bin`, []string{`C:\b\1.bin`}}, `C:\b\1.bin`, ""},
		{"conservar lo mismo que se borra", DuplicateChoice{`C:\a\1.bin`, []string{`C:\A\1.BIN`}}, `C:\A\1.BIN`, actions.CodeNoCopyLeft},
		{"la lista incluye la que queda", DuplicateChoice{`C:\a\1.bin`, []string{`C:\b\1.bin`, `C:\a\1.bin`}}, `C:\b\1.bin`, actions.CodeNoCopyLeft},
		{"de otro grupo", DuplicateChoice{`C:\a\1.bin`, []string{`C:\b\2.bin`}}, `C:\b\2.bin`, actions.CodeNotDuplicate},
		{"la que queda no existe", DuplicateChoice{`C:\z.bin`, []string{`C:\b\1.bin`}}, `C:\b\1.bin`, actions.CodeNotDuplicate},
		{"ruta inventada", DuplicateChoice{`C:\a\1.bin`, []string{`C:\Windows\x.dll`}}, `C:\Windows\x.dll`, actions.CodeNotDuplicate},
		{"rastreada por git", DuplicateChoice{`C:\a\1.bin`, []string{`C:\c\1.bin`}}, `C:\c\1.bin`, actions.CodeKeep},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, tasks := planChoices(testReport(), []DuplicateChoice{c.choice})
			if got := planCode(tasks, c.path); got != c.want {
				t.Fatalf("código = %q, se esperaba %q", got, c.want)
			}
		})
	}
}

// Dos decisiones cruzadas no pueden sumarse para borrar todas las copias.
func TestPlanChoicesRefusesCrossedChoices(t *testing.T) {
	_, tasks := planChoices(testReport(), []DuplicateChoice{
		{`C:\a\2.bin`, []string{`C:\b\2.bin`}},
		{`C:\b\2.bin`, []string{`C:\a\2.bin`}},
	})
	if got := planCode(tasks, `C:\b\2.bin`); got != "" {
		t.Fatalf("primera: %q", got)
	}
	if got := planCode(tasks, `C:\a\2.bin`); got != actions.CodeNoCopyLeft {
		t.Fatalf("segunda: %q", got)
	}
}

func waitClean(t *testing.T, service *DuplicateService) CleanStatus {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if status := service.CleanStatus(); status.State == cleanDone {
			return status
		}
	}
	t.Fatal("la limpieza no terminó")
	return CleanStatus{}
}

func restoreFromBin(t *testing.T, original string) {
	t.Helper()
	script := fmt.Sprintf(`
$bin = (New-Object -ComObject Shell.Application).Namespace(10)
$bin.Items() | Where-Object { $bin.GetDetailsOf($_, 1) -eq '%s' } | ForEach-Object { $_.InvokeVerb('undelete') }`,
		strings.ReplaceAll(filepath.Dir(original), "'", "''"))
	if out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput(); err != nil {
		t.Fatalf("restaurar: %v: %s", err, out)
	}
	if _, err := os.Lstat(original); err != nil {
		t.Fatalf("no se restauró %s: %v", original, err)
	}
}

// Ida y vuelta real: una copia a la papelera, la otra intacta, y se restaura. La
// papelera oculta extensiones, así que restoreFromBin busca por carpeta original.
func TestCleanRealRoundTrip(t *testing.T) {
	same := dupBytes('a')
	root := dupFixture(t, map[string][]byte{"fotos/a.jpg": same, "respaldo/a.jpg": same})
	keep, remove := filepath.Join(root, "fotos", "a.jpg"), filepath.Join(root, "respaldo", "a.jpg")
	service := searched(t, root)

	if err := service.Clean([]DuplicateChoice{{Keep: keep, Delete: []string{remove}}}); err != nil {
		t.Fatal(err)
	}
	status := waitClean(t, service)
	if len(status.Outcomes) != 1 || status.Outcomes[0].Status != actions.StatusDone {
		t.Fatalf("%+v", status)
	}
	t.Cleanup(func() { restoreFromBin(t, remove) })
	if _, err := os.Lstat(remove); !os.IsNotExist(err) {
		t.Fatalf("la copia sigue ahí: %v", err)
	}
	if got, err := os.ReadFile(keep); err != nil || len(got) != dupSize {
		t.Fatalf("la que se conserva: %d, %v", len(got), err)
	}
	if report := service.Report(); len(report.Groups) != 0 || report.Reclaimable != 0 {
		t.Fatalf("debe salir del reporte: %+v", report)
	}
}

func TestCleanRefusesWhenKeepChangedAfterSearch(t *testing.T) {
	same := dupBytes('a')
	root := dupFixture(t, map[string][]byte{"fotos/a.jpg": same, "respaldo/a.jpg": same})
	keep, other := filepath.Join(root, "fotos", "a.jpg"), filepath.Join(root, "respaldo", "a.jpg")
	service := searched(t, root)

	changed := dupBytes('a')
	changed[dupSize/2] = 'x'
	if err := os.WriteFile(keep, changed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.Clean([]DuplicateChoice{{Keep: keep, Delete: []string{other}}}); err != nil {
		t.Fatal(err)
	}
	status := waitClean(t, service)
	if status.Outcomes[0].Code != actions.CodeNoCopyLeft {
		t.Fatalf("%+v", status.Outcomes)
	}
	if _, err := os.Lstat(other); err != nil {
		t.Fatalf("no debía tocarla: %v", err)
	}
	if len(service.Report().Groups) != 1 {
		t.Fatal("el grupo debe seguir en el reporte")
	}
}

func TestCleanRefusesProtectedAndInventedPaths(t *testing.T) {
	same := dupBytes('a')
	root := dupFixture(t, map[string][]byte{"fotos/a.jpg": same, "respaldo/a.jpg": same})
	keep := filepath.Join(root, "fotos", "a.jpg")
	service := searched(t, root)
	outside := filepath.Join(root, "fuera.bin")
	if err := os.WriteFile(outside, same, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := service.Clean([]DuplicateChoice{{Keep: keep, Delete: []string{outside}}}); err != nil {
		t.Fatal(err)
	}
	status := waitClean(t, service)
	if status.Outcomes[0].Status == actions.StatusDone {
		t.Fatalf("%+v", status.Outcomes)
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("no debía tocarlo: %v", err)
	}
}

func TestNewScanInvalidatesReport(t *testing.T) {
	same := dupBytes('a')
	root := dupFixture(t, map[string][]byte{"a/x.bin": same, "b/x.bin": same})
	service := searched(t, root)
	if len(service.Report().Groups) != 1 {
		t.Fatal("debía encontrar el grupo")
	}
	service.scan.mu.Lock()
	service.scan.generation++
	service.scan.mu.Unlock()
	if status := service.Status(); status.State != dupIdle {
		t.Fatalf("%+v", status)
	}
	if report := service.Report(); len(report.Groups) != 0 {
		t.Fatalf("%+v", report)
	}
	err := service.Clean([]DuplicateChoice{{Keep: filepath.Join(root, "a", "x.bin"), Delete: []string{filepath.Join(root, "b", "x.bin")}}})
	if err == nil {
		t.Fatal("no debía limpiar con un reporte viejo")
	}
}

func TestCandidatesStartOnlyWithFinishedScan(t *testing.T) {
	service := &DuplicateService{scan: &ScanService{}}
	if err := service.Start(nil); err == nil {
		t.Fatal("debía rechazar sin escaneo")
	}
	service = &DuplicateService{scan: &ScanService{state: stateScanning}}
	if err := service.Start(nil); err == nil {
		t.Fatal("debía rechazar mientras escanea")
	}
}

func TestCleanRefusesOverflowAndKeepsCopy(t *testing.T) {
	binRoomOverride = 0
	t.Cleanup(func() { binRoomOverride = -1 })

	same := dupBytes('a')
	root := dupFixture(t, map[string][]byte{"fotos/a.jpg": same, "respaldo/a.jpg": same})
	keep, other := filepath.Join(root, "fotos", "a.jpg"), filepath.Join(root, "respaldo", "a.jpg")
	service := searched(t, root)

	if err := service.Clean([]DuplicateChoice{{Keep: keep, Delete: []string{other}}}); err != nil {
		t.Fatal(err)
	}
	status := waitClean(t, service)
	if status.Outcomes[0].Code != actions.CodeBinFull {
		t.Fatalf("%+v", status.Outcomes)
	}
	if _, err := os.Lstat(other); err != nil {
		t.Fatalf("no debía tocarla: %v", err)
	}
}

func TestInFoldersRespectsBoundaries(t *testing.T) {
	folders := []string{`C:\Fotos`, `D:\Respaldo\`}
	for _, path := range []string{`C:\Fotos\a.jpg`, `c:\fotos\2019\a.jpg`, `D:\Respaldo\a.jpg`} {
		if !inFolders(path, folders) {
			t.Errorf("%s debía entrar", path)
		}
	}
	for _, path := range []string{`C:\Fotos2\a.jpg`, `C:\Otra\Fotos\a.jpg`, `D:\a.jpg`} {
		if inFolders(path, folders) {
			t.Errorf("%s no debía entrar", path)
		}
	}
	if !inFolders(`C:\x`, nil) {
		t.Error("sin carpetas vale todo")
	}
}

// Solo compara entre las carpetas elegidas: una copia afuera no cuenta.
func TestSearchOnlyInChosenFolders(t *testing.T) {
	same, other := dupBytes('a'), dupBytes('b')
	root := dupFixture(t, map[string][]byte{
		"fotos/2019/a.jpg": same,
		"respaldo/a.jpg":   same,
		"afuera/a.jpg":     same,
		"afuera/b.jpg":     other,
		"afuera2/b.jpg":    other,
	})
	report := searched(t, root, filepath.Join(root, "fotos"), filepath.Join(root, "respaldo")).Report()
	if len(report.Groups) != 1 || len(report.Groups[0].Copies) != 2 {
		t.Fatalf("%+v", report.Groups)
	}
	for _, item := range report.Groups[0].Copies {
		if strings.Contains(item.Path, "afuera") {
			t.Fatalf("no debía compararse: %s", item.Path)
		}
	}
}
