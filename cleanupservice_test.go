package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"el-buscador/internal/actions"
	"el-buscador/internal/devtrash"
	"el-buscador/internal/rules"
	"el-buscador/internal/scanner"
)

// scannedService escanea una carpeta temporal. Está dentro de %LOCALAPPDATA%\Temp, así
// que el catálogo real la marca como Seguro.
func scannedService(t *testing.T, root string, findings ...devtrash.Finding) *CleanupService {
	t.Helper()
	result, err := scanner.Scan(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]devtrash.Finding)
	for _, finding := range findings {
		byPath[finding.Path] = finding
	}
	scan := &ScanService{state: stateDone, result: result, catalog: rules.New(rules.EnvFromOS()), findings: byPath}
	return &CleanupService{scan: scan}
}

func fixture(t *testing.T, names ...string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func cacheFinding(path string, command ...string) devtrash.Finding {
	return devtrash.Finding{
		Path: path, Category: devtrash.CategoryCache,
		Advice:  rules.Advice{Level: rules.LevelSafe},
		Command: command,
	}
}

func waitDone(t *testing.T, service *CleanupService) CleanStatus {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if status := service.Status(); status.State == cleanDone {
			return status
		}
	}
	t.Fatal("la limpieza no terminó")
	return CleanStatus{}
}

func TestPlanRefusesWithoutScan(t *testing.T) {
	root := fixture(t, "a.txt")
	service := &CleanupService{scan: &ScanService{}}
	plan, err := service.Plan([]string{filepath.Join(root, "a.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Code != actions.CodeNotScanned || plan.Total != 0 {
		t.Fatalf("%+v", plan)
	}
}

func TestPlanOnlyAllowsScannedPaths(t *testing.T) {
	root := fixture(t, "carpeta/a.txt")
	service := scannedService(t, root)
	late := filepath.Join(root, "carpeta", "despues.txt")
	if err := os.WriteFile(late, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := service.Plan([]string{late, filepath.Join(root, "carpeta", "a.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Code != "" || plan.Items[1].Code != actions.CodeNotScanned {
		t.Fatalf("%+v", plan.Items)
	}
}

func TestPlanCollapsesNestedSelection(t *testing.T) {
	root := fixture(t, "carpeta/a.txt")
	folder := filepath.Join(root, "carpeta")
	plan, err := scannedService(t, root).Plan([]string{filepath.Join(folder, "a.txt"), folder})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Path != folder {
		t.Fatalf("%+v", plan.Items)
	}
}

func TestPlanPrefersCommandAndAdmitsMissingTool(t *testing.T) {
	root := fixture(t, "con/a.bin", "sin/a.bin")
	with, without := filepath.Join(root, "con"), filepath.Join(root, "sin")
	service := scannedService(t, root,
		cacheFinding(with, "cmd", "/c", "echo hola"),
		cacheFinding(without, "el-buscador-no-existe", "limpia"),
	)

	plan, err := service.Plan([]string{with, without})
	if err != nil {
		t.Fatal(err)
	}
	first, second := plan.Items[0], plan.Items[1]
	if first.Method != actions.MethodCommand || first.Command != "cmd /c echo hola" {
		t.Fatalf("con herramienta: %+v", first)
	}
	if second.Method != actions.MethodTrash || second.MissingTool != "el-buscador-no-existe" {
		t.Fatalf("sin herramienta: %+v", second)
	}
}

func TestStartRunsOnlyTheScannedCommand(t *testing.T) {
	root := fixture(t, "cache/a.bin", "otra/a.bin")
	cache, other := filepath.Join(root, "cache"), filepath.Join(root, "otra")
	service := scannedService(t, root, cacheFinding(cache, "cmd", "/c", "echo hola"))

	// Pedir un comando para algo sin comando no debe caer en borrarlo.
	err := service.Start([]CleanRequest{
		{Path: cache, Method: actions.MethodCommand},
		{Path: other, Method: actions.MethodCommand},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := waitDone(t, service)
	if status.Outcomes[0].Status != actions.StatusDone || !strings.Contains(status.Output, "hola") {
		t.Fatalf("%+v", status)
	}
	if status.Outcomes[1].Status == actions.StatusDone {
		t.Fatalf("%+v", status.Outcomes[1])
	}
	for _, path := range []string{cache, other} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func TestStartRefusesNotScannedAndKeepsGoing(t *testing.T) {
	root := fixture(t, "a.bin")
	service := scannedService(t, root)
	late := filepath.Join(root, "despues.bin")
	if err := os.WriteFile(late, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := service.Start([]CleanRequest{{Path: late, Method: actions.MethodTrash}}); err != nil {
		t.Fatal(err)
	}
	status := waitDone(t, service)
	if status.Outcomes[0].Code != actions.CodeNotScanned {
		t.Fatalf("%+v", status.Outcomes)
	}
	if _, err := os.Lstat(late); err != nil {
		t.Fatal(err)
	}
}

func TestStartRefusedWhileScanning(t *testing.T) {
	service := &CleanupService{scan: &ScanService{state: stateScanning}}
	if err := service.Start([]CleanRequest{}); err == nil {
		t.Fatal("debía rechazar mientras escanea")
	}
}

func TestOutputKeepsOnlyTheEnd(t *testing.T) {
	service := &CleanupService{scan: &ScanService{}}
	writer := cleanOutput{&service.job}
	writer.Write([]byte(strings.Repeat("a", maxCleanOutput)))
	writer.Write([]byte("fin"))
	output := service.Status().Output
	if len(output) != maxCleanOutput || !strings.HasSuffix(output, "fin") {
		t.Fatalf("len = %d", len(output))
	}
}

func TestServiceDoesNotExposeWriteToFrontend(t *testing.T) {
	if _, ok := any(&CleanupService{}).(interface{ Write([]byte) (int, error) }); ok {
		t.Fatal("Wails expondría Write a la interfaz")
	}
}

func TestPlanReportsBinAndStartRefusesOverflow(t *testing.T) {
	binRoomOverride = 0
	t.Cleanup(func() { binRoomOverride = -1 })

	root := fixture(t, "a.bin")
	path := filepath.Join(root, "a.bin")
	service := scannedService(t, root)

	plan, err := service.Plan([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Bin.Room != 0 || plan.TrashTotal != plan.Items[0].Size {
		t.Fatalf("%+v", plan)
	}
	if err := service.Start([]CleanRequest{{Path: path, Method: actions.MethodTrash}}); err != nil {
		t.Fatal(err)
	}
	status := waitDone(t, service)
	if status.Outcomes[0].Code != actions.CodeBinFull {
		t.Fatalf("%+v", status.Outcomes)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("no debía tocarlo: %v", err)
	}
}

func TestPlanCommandDoesNotCountForBin(t *testing.T) {
	root := fixture(t, "cache/a.bin")
	cache := filepath.Join(root, "cache")
	plan, err := scannedService(t, root, cacheFinding(cache, "cmd", "/c", "echo hola")).Plan([]string{cache})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TrashTotal != 0 {
		t.Fatalf("%+v", plan)
	}
}

func TestFreeUpRefusesOutsideSyncAndScan(t *testing.T) {
	root := fixture(t, "carpeta/a.bin")
	folder := filepath.Join(root, "carpeta")
	service := scannedService(t, root)
	// La carpeta temporal no se sincroniza: ni la vista previa ni la acción avanzan.
	if got := service.FreeUpPreview(folder); got.Code != actions.CodeNotSynced || got.Files != 0 {
		t.Fatalf("%+v", got)
	}
	if got := service.FreeUp(folder); got.Code != actions.CodeNotSynced || got.Files != 0 {
		t.Fatalf("%+v", got)
	}
	if got := service.FreeUp(filepath.Join(root, "no-escaneada")); got.Code != actions.CodeNotScanned {
		t.Fatalf("%+v", got)
	}
	if got := (&CleanupService{scan: &ScanService{}}).FreeUp(folder); got.Code != actions.CodeNotScanned {
		t.Fatalf("%+v", got)
	}
}
