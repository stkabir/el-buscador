package actions

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"el-buscador/internal/rules"
)

func safeAdvice(string, bool) (rules.Advice, bool) {
	return rules.Advice{Level: rules.LevelSafe}, true
}

// testEnv simula un sistema dentro de la carpeta temporal, salvo el disco.
func testEnv(root string) rules.Env {
	return rules.Env{
		SystemDrive:  filepath.VolumeName(root),
		Windir:       filepath.Join(root, "Windows"),
		ProgramData:  filepath.Join(root, "ProgramData"),
		ProgramFiles: []string{filepath.Join(root, "Program Files")},
		Home:         filepath.Join(root, "Users", "ana"),
		LocalAppData: filepath.Join(root, "Users", "ana", "AppData", "Local"),
		AppData:      filepath.Join(root, "Users", "ana", "AppData", "Roaming"),
	}
}

func wantCode(t *testing.T, err error, want Code) {
	t.Helper()
	if got := CodeOf(err); got != want {
		t.Fatalf("código = %q (%v), se esperaba %q", got, err, want)
	}
}

func TestGuardBlocksSystemPaths(t *testing.T) {
	root := tempDir(t)
	env := testEnv(root)
	guard := Guard{Env: env, Classify: safeAdvice}
	drive := env.SystemDrive

	blocked := []string{
		drive + `\`,
		root + `\Windows`,
		root + `\Windows\System32`,
		root + `\windows\TEMP\x`,
		root + `\Program Files`,
		root + `\Program Files\App\app.exe`,
		root + `\ProgramData`,
		root + `\ProgramData\Docker`,
		root + `\Users`,
		root + `\Users\ana`,
		root + `\Users\beto`,
		root + `\Users\ana\Documents`,
		root + `\Users\ana\Desktop`,
		root + `\Users\ana\AppData`,
		root + `\Users\ana\AppData\Local`,
		root + `\Users\ana\AppData\Roaming`,
		root + `\Users\ana\iCloudDrive`,
		root + `\Users\ana\OneDrive - Empresa`,
		drive + `\$Recycle.Bin`,
		drive + `\$Recycle.Bin\S-1-5\$R123`,
		drive + `\System Volume Information`,
		drive + `\pagefile.sys`,
		drive + `\hiberfil.sys`,
		// Contiene carpetas vitales del sistema simulado.
		root,
		filepath.Dir(root),
		`Z:\proyecto\node_modules`,
	}
	for _, path := range blocked {
		_, err := guard.Check(path, true)
		if CodeOf(err) != CodeProtected {
			t.Errorf("%s: %v, debía estar protegida", path, err)
		}
	}
	// Los archivos reales de usuario dentro de estas carpetas sí se pueden revisar.
	inside := root + `\Users\ana\Documents\viejo.iso`
	writeFile(t, inside, "x")
	if _, err := guard.Check(inside, true); err != nil {
		t.Errorf("%s: %v", inside, err)
	}
}

func TestGuardRejectsMalformedPaths(t *testing.T) {
	root := tempDir(t)
	guard := Guard{Env: testEnv(root), Classify: safeAdvice}
	file := filepath.Join(root, "a.txt")
	writeFile(t, file, "x")

	for _, path := range []string{
		"", "a.txt", `\a.txt`, `C:a.txt`, `\\servidor\carpeta\a.txt`, `\\?\` + file,
		file + `:oculto`, root + `\*`, root + `\..\a.txt`, root + `\a.txt\`, strings.ReplaceAll(file, `\`, `/`),
	} {
		_, err := guard.Check(path, true)
		if CodeOf(err) != CodeInvalidPath {
			t.Errorf("%q: %v", path, err)
		}
	}
}

func TestGuardFailsClosedWithoutEnv(t *testing.T) {
	root := tempDir(t)
	file := filepath.Join(root, "a.txt")
	writeFile(t, file, "x")
	_, err := Guard{Classify: safeAdvice}.Check(file, true)
	wantCode(t, err, CodeProtected)
}

func TestGuardMissingPath(t *testing.T) {
	root := tempDir(t)
	_, err := Guard{Env: testEnv(root), Classify: safeAdvice}.Check(filepath.Join(root, "no-existe"), true)
	wantCode(t, err, CodeMissing)
}

func TestGuardRejectsLinks(t *testing.T) {
	root := tempDir(t)
	guard := Guard{Env: testEnv(root), Classify: safeAdvice}
	target := filepath.Join(root, "importante")
	writeFile(t, filepath.Join(target, "dato.txt"), "x")

	// Los junctions no requieren permisos especiales.
	junction := filepath.Join(root, "enlace")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v: %s", err, out)
	}
	_, err := guard.Check(junction, true)
	wantCode(t, err, CodeLink)

	// Entrar por un enlace en un padre tampoco cuenta como la ruta real.
	_, err = guard.Check(filepath.Join(junction, "dato.txt"), true)
	wantCode(t, err, CodeLink)

	if err := os.Symlink(target, filepath.Join(root, "symlink")); err == nil {
		_, err := guard.Check(filepath.Join(root, "symlink"), true)
		wantCode(t, err, CodeLink)
	} else {
		t.Logf("symlink sin probar: %v", err)
	}
}

func TestGuardLevels(t *testing.T) {
	root := tempDir(t)
	file := filepath.Join(root, "a.txt")
	writeFile(t, file, "x")

	cases := []struct {
		name      string
		advice    rules.Advice
		ok        bool
		confirmed bool
		want      Code
	}{
		{"seguro", rules.Advice{Level: rules.LevelSafe}, true, false, ""},
		{"revisar sin confirmar", rules.Advice{Level: rules.LevelReview}, true, false, CodeNeedsConfirm},
		{"revisar confirmado", rules.Advice{Level: rules.LevelReview}, true, true, ""},
		{"no tocar", rules.Advice{Level: rules.LevelKeep}, true, true, CodeKeep},
		{"sin clasificar", rules.Advice{}, false, true, CodeUnclassified},
		{"nivel desconocido", rules.Advice{Level: "raro"}, true, true, CodeUnclassified},
		{"se quita desde su app", rules.Advice{Level: rules.LevelReview, Manual: true}, true, true, CodeManual},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			guard := Guard{Env: testEnv(root), Classify: func(string, bool) (rules.Advice, bool) { return c.advice, c.ok }}
			_, err := guard.Check(file, c.confirmed)
			wantCode(t, err, c.want)
		})
	}
}

func TestGuardComparesAgainstScan(t *testing.T) {
	root := tempDir(t)
	path := filepath.Join(root, "antes-archivo")
	writeFile(t, filepath.Join(path, "ahora-carpeta.txt"), "x")

	guard := Guard{Env: testEnv(root), Classify: safeAdvice}
	guard.Scanned = func(string) (bool, bool) { return false, false }
	_, err := guard.Check(path, true)
	wantCode(t, err, CodeNotScanned)

	guard.Scanned = func(string) (bool, bool) { return false, true }
	_, err = guard.Check(path, true)
	wantCode(t, err, CodeChanged)

	guard.Scanned = func(string) (bool, bool) { return true, true }
	if _, err := guard.Check(path, true); err != nil {
		t.Fatal(err)
	}
}

func TestCleanerRefusesUnknownMethod(t *testing.T) {
	root := tempDir(t)
	path := filepath.Join(root, "a.txt")
	writeFile(t, path, "x")
	var trashed []string
	outcome := run(context.Background(), fakeCleaner(root, &trashed), []Request{{Path: path, Method: "otro"}})[0]
	if outcome.Status == StatusDone || len(trashed) != 0 {
		t.Fatalf("%+v, trashed = %v", outcome, trashed)
	}
}

func TestBinAllows(t *testing.T) {
	wantCode(t, binAllows(true, 100, 1), CodeBinOff)
	wantCode(t, binAllows(false, 100, 100), CodeTooBig)
	wantCode(t, binAllows(false, 100, 101), CodeTooBig)
	wantCode(t, binAllows(false, 100, 99), "")
}

func TestCheckRecycleBinReadsThisSystem(t *testing.T) {
	root := tempDir(t)
	// Mil terabytes no caben en la papelera de ningún disco de este equipo.
	wantCode(t, checkRecycleBin(root, 1<<50), CodeTooBig)
}

func TestMeasureDoesNotFollowLinks(t *testing.T) {
	root := tempDir(t)
	writeFile(t, filepath.Join(root, "afuera", "grande.bin"), strings.Repeat("x", 1000))
	writeFile(t, filepath.Join(root, "carpeta", "chico.bin"), "12345")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "carpeta", "enlace"), filepath.Join(root, "afuera")).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v: %s", err, out)
	}
	size, err := measure(context.Background(), filepath.Join(root, "carpeta"))
	if err != nil || size != 5 {
		t.Fatalf("size = %d, %v", size, err)
	}
}

func TestCollapseNested(t *testing.T) {
	got := CollapseNested([]string{`C:\a\b`, `C:\a`, `c:\A\c.txt`, `C:\ab`, `C:\z`, `C:\z`})
	want := []string{`C:\a`, `C:\ab`, `C:\z`}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

// fakeCleaner no toca nada: registra lo que habría mandado a la papelera.
func fakeCleaner(root string, trashed *[]string) Cleaner {
	return Cleaner{
		Guard: Guard{Env: testEnv(root), Classify: safeAdvice},
		Trash: func(path string, _ func() bool) (string, error) {
			*trashed = append(*trashed, path)
			return `C:\$Recycle.Bin\x`, nil
		},
		Output: io.Discard,
	}
}

func run(ctx context.Context, cleaner Cleaner, requests []Request) []Outcome {
	var outcomes []Outcome
	cleaner.Run(ctx, requests, func(string) {}, func(o Outcome) { outcomes = append(outcomes, o) })
	return outcomes
}

func TestCleanerContinuesAfterFailure(t *testing.T) {
	root := tempDir(t)
	good := filepath.Join(root, "bueno.txt")
	writeFile(t, good, "12345")
	missing := filepath.Join(root, "no-existe.txt")
	var trashed []string

	outcomes := run(context.Background(), fakeCleaner(root, &trashed), []Request{
		{Path: missing, Method: MethodTrash},
		{Path: good, Method: MethodTrash},
	})
	if outcomes[0].Status != StatusRefused || outcomes[0].Code != CodeMissing {
		t.Fatalf("faltante: %+v", outcomes[0])
	}
	if outcomes[1].Status != StatusDone || outcomes[1].Trashed != 5 {
		t.Fatalf("bueno: %+v", outcomes[1])
	}
	if !slices.Equal(trashed, []string{good}) {
		t.Fatalf("trashed = %v", trashed)
	}
}

// El semáforo se vuelve a leer al ejecutar: lo aprobado en el escaneo no basta.
func TestCleanerRefusesPathThatChangedAfterPlan(t *testing.T) {
	root := tempDir(t)
	path := filepath.Join(root, "dist")
	writeFile(t, filepath.Join(path, "a.bin"), "x")
	var trashed []string
	cleaner := fakeCleaner(root, &trashed)
	cleaner.Guard.Classify = func(string, bool) (rules.Advice, bool) {
		return rules.Advice{Level: rules.LevelKeep}, true
	}

	outcomes := run(context.Background(), cleaner, []Request{{Path: path, Method: MethodTrash, ConfirmedReview: true}})
	if outcomes[0].Code != CodeKeep || len(trashed) != 0 {
		t.Fatalf("%+v, trashed = %v", outcomes[0], trashed)
	}
}

func TestCleanerCancelMidway(t *testing.T) {
	root := tempDir(t)
	first, second := filepath.Join(root, "1.txt"), filepath.Join(root, "2.txt")
	writeFile(t, first, "x")
	writeFile(t, second, "x")

	ctx, cancel := context.WithCancel(context.Background())
	var trashed []string
	cleaner := fakeCleaner(root, &trashed)
	inner := cleaner.Trash
	cleaner.Trash = func(path string, canceled func() bool) (string, error) {
		defer cancel()
		return inner(path, canceled)
	}

	outcomes := run(ctx, cleaner, []Request{{Path: first, Method: MethodTrash}, {Path: second, Method: MethodTrash}})
	if outcomes[0].Status != StatusDone || outcomes[1].Status != StatusCanceled {
		t.Fatalf("%+v", outcomes)
	}
	if !slices.Equal(trashed, []string{first}) {
		t.Fatalf("trashed = %v", trashed)
	}
}

func TestCleanerTrashErrorsBecomeCodes(t *testing.T) {
	root := tempDir(t)
	path := filepath.Join(root, "a.txt")
	writeFile(t, path, "x")

	cases := map[error]Code{
		ErrInUse:                  CodeInUse,
		ErrWouldDeletePermanently: CodeBinRejected,
		ErrCanceled:               CodeCanceled,
		os.ErrPermission:          CodeNoPermission,
		errors.New("otro"):        CodeFailed,
	}
	for failure, want := range cases {
		cleaner := Cleaner{
			Guard: Guard{Env: testEnv(root), Classify: safeAdvice},
			Trash: func(string, func() bool) (string, error) { return "", failure },
		}
		outcome := run(context.Background(), cleaner, []Request{{Path: path, Method: MethodTrash}})[0]
		if outcome.Code != want || outcome.Status == StatusDone {
			t.Errorf("%v: %+v", failure, outcome)
		}
	}
}

// Sin la herramienta no se cae a la papelera: el usuario aprobó un comando.
func TestCleanerCommandToolMissingDoesNotFallBack(t *testing.T) {
	root := tempDir(t)
	path := filepath.Join(root, "cache")
	writeFile(t, filepath.Join(path, "a"), "x")
	var trashed []string
	cleaner := fakeCleaner(root, &trashed)
	cleaner.Command = func(ctx context.Context, argv []string, output io.Writer) error {
		return RunCommand(ctx, argv, root, output)
	}

	outcome := run(context.Background(), cleaner, []Request{{Path: path, Method: MethodCommand, Command: []string{"el-buscador-no-existe"}}})[0]
	if outcome.Code != CodeToolMissing || outcome.Status != StatusFailed || len(trashed) != 0 {
		t.Fatalf("%+v, trashed = %v", outcome, trashed)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatal(err)
	}
}

func TestRunCommandOutputAndFailure(t *testing.T) {
	dir := tempDir(t)
	var output bytes.Buffer
	if err := RunCommand(context.Background(), []string{"cmd", "/c", "echo hola"}, dir, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "hola") {
		t.Fatalf("salida: %q", output.String())
	}

	err := RunCommand(context.Background(), []string{"cmd", "/c", "exit 3"}, dir, &output)
	wantCode(t, err, CodeToolFailed)
	if !strings.Contains(output.String(), "exit 3") {
		t.Fatalf("salida: %q", output.String())
	}
}

// Sin shell, los metacaracteres son texto: "&" no encadena otro programa.
func TestRunCommandDoesNotUseShell(t *testing.T) {
	dir := tempDir(t)
	marker := filepath.Join(dir, "inyectado.txt")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	_ = RunCommand(context.Background(), []string{exe, "-test.run=^$", "&", "cmd", "/c", "echo x>" + marker}, dir, &output)
	if _, err := os.Lstat(marker); err == nil {
		t.Fatal("se ejecutó el comando inyectado")
	}
}

func TestRunCommandCancel(t *testing.T) {
	dir := tempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- RunCommand(ctx, []string{"ping", "-n", "30", "127.0.0.1"}, dir, io.Discard)
	}()
	time.Sleep(500 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		wantCode(t, err, CodeCanceled)
	case <-time.After(15 * time.Second):
		t.Fatalf("no se detuvo tras %s", time.Since(start))
	}
}

func TestRevealCmdLineQuotesPath(t *testing.T) {
	got := revealCmdLine(`C:\Mis cosas\a b.txt`)
	if got != `explorer.exe /select,"C:\Mis cosas\a b.txt"` {
		t.Fatal(got)
	}
	if err := Reveal(`\\servidor\x`); CodeOf(err) != CodeInvalidPath {
		t.Fatalf("err = %v", err)
	}
}

// Prueba de punta a punta con la papelera real, y restaurando lo enviado.
func TestCleanerRealTrashRoundTrip(t *testing.T) {
	root := tempDir(t)
	path := filepath.Join(root, "node_modules")
	writeFile(t, filepath.Join(path, "pkg", "index.js"), "module.exports = 1")

	var recycled string
	cleaner := Cleaner{
		Guard: Guard{Env: testEnv(root), Classify: safeAdvice},
		Trash: func(p string, canceled func() bool) (string, error) {
			out, err := MoveToTrash(p, canceled)
			recycled = out
			return out, err
		},
	}
	outcome := run(context.Background(), cleaner, []Request{{Path: path, Method: MethodTrash}})[0]
	if outcome.Status != StatusDone || outcome.Trashed != 18 {
		t.Fatalf("%+v", outcome)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sigue en su lugar: %v", err)
	}
	restore(t, recycled)
	if got, err := os.ReadFile(filepath.Join(path, "pkg", "index.js")); err != nil || string(got) != "module.exports = 1" {
		t.Fatalf("tras restaurar: %q, %v", got, err)
	}
}
