package devtrash

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"el-buscador/internal/rules"
	"el-buscador/internal/scanner"
)

const trashSize = minFindingSize + 4096

func write(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git no instalado")
	}
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func analyze(t *testing.T, root string, env rules.Env) Report {
	t.Helper()
	result, err := scanner.Scan(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Analyze(context.Background(), result, rules.New(env))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func only(t *testing.T, report Report) Finding {
	t.Helper()
	if len(report.Findings) != 1 {
		t.Fatalf("Findings = %+v, quiero 1", report.Findings)
	}
	return report.Findings[0]
}

func TestTrackedFolderIsNeverSafe(t *testing.T) {
	for _, name := range []string{"vendor", "dist"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "composer.json"), 2)
			write(t, filepath.Join(root, "composer.lock"), 2)
			write(t, filepath.Join(root, "package.json"), 2)
			write(t, filepath.Join(root, "pnpm-lock.yaml"), 2)
			write(t, filepath.Join(root, name, "pkg.bin"), trashSize)
			git(t, root, "init", "-q")
			git(t, root, "add", ".")

			finding := only(t, analyze(t, root, rules.Env{}))
			if finding.Advice.Level != rules.LevelKeep {
				t.Errorf("Level = %s, quiero keep", finding.Advice.Level)
			}
		})
	}
}

func TestIgnoredFolderIsSafe(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "composer.json"), 2)
	write(t, filepath.Join(root, ".gitignore"), 0)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/vendor/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "vendor", "pkg.bin"), trashSize)
	git(t, root, "init", "-q")

	report := analyze(t, root, rules.Env{})
	finding := only(t, report)
	if finding.Advice.Level != rules.LevelSafe {
		t.Errorf("Level = %s, quiero safe: %s", finding.Advice.Level, finding.Advice.Reason.ES)
	}
	if report.SafeSize != finding.Size {
		t.Errorf("SafeSize = %d, quiero %d", report.SafeSize, finding.Size)
	}
}

func TestUntrackedFolderNeedsLockfileToBeSafe(t *testing.T) {
	tests := []struct {
		name string
		lock bool
		want rules.Level
	}{
		{"sin lockfile", false, rules.LevelReview},
		{"con lockfile", true, rules.LevelSafe},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "package.json"), 2)
			write(t, filepath.Join(root, "node_modules", "dep.bin"), trashSize)
			if test.lock {
				write(t, filepath.Join(root, "package-lock.json"), 2)
			}
			git(t, root, "init", "-q")

			finding := only(t, analyze(t, root, rules.Env{}))
			if finding.Advice.Level != test.want {
				t.Errorf("Level = %s, quiero %s", finding.Advice.Level, test.want)
			}
		})
	}
}

func TestNoRepoWithoutLockfileIsReview(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "package.json"), 2)
	write(t, filepath.Join(root, "node_modules", "dep.bin"), trashSize)

	if level := only(t, analyze(t, root, rules.Env{})).Advice.Level; level != rules.LevelReview {
		t.Errorf("Level = %s, quiero review", level)
	}
}

func TestNameWithoutMarkerIsIgnored(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "target", "mis-fotos.bin"), trashSize)
	write(t, filepath.Join(root, "vendor", "facturas.bin"), trashSize)
	write(t, filepath.Join(root, "dist", "entrega.bin"), trashSize)

	if report := analyze(t, root, rules.Env{}); len(report.Findings) != 0 {
		t.Errorf("Findings = %+v", report.Findings)
	}
}

func TestGitFailureIsNeverSafe(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "package.json"), 2)
	write(t, filepath.Join(root, "package-lock.json"), 2)
	write(t, filepath.Join(root, "node_modules", "dep.bin"), trashSize)
	// Un .git archivo con basura hace fallar a git.
	write(t, filepath.Join(root, ".git"), 3)

	if level := only(t, analyze(t, root, rules.Env{})).Advice.Level; level != rules.LevelReview {
		t.Errorf("Level = %s, quiero review", level)
	}

	t.Run("git no instalado", func(t *testing.T) {
		t.Setenv("PATH", "")
		if level := only(t, analyze(t, root, rules.Env{})).Advice.Level; level != rules.LevelReview {
			t.Errorf("Level = %s, quiero review", level)
		}
	})
}

func TestSyncedFolderIsNotSafe(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "OneDrive", "app")
	write(t, filepath.Join(project, "package.json"), 2)
	write(t, filepath.Join(project, "package-lock.json"), 2)
	write(t, filepath.Join(project, "node_modules", "dep.bin"), trashSize)

	finding := only(t, analyze(t, home, rules.Env{Home: home}))
	if finding.Advice.Level != rules.LevelReview {
		t.Errorf("Level = %s, quiero review", finding.Advice.Level)
	}
	if !strings.Contains(finding.Advice.Reason.ES, "sincroniza") {
		t.Errorf("Reason = %s", finding.Advice.Reason.ES)
	}
}

// Las apps instaladas y las extensiones traen node_modules con package.json, pero
// borrarlos las rompe.
func TestSkipsInstalledAppFolders(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{`AppData\Roaming\npm`, `Program Files\app`, `.vscode\extensions\ext`, `.windsurf\extensions\ext`, `tools\editor\resources\app`} {
		write(t, filepath.Join(root, dir, "package.json"), 2)
		write(t, filepath.Join(root, dir, "package-lock.json"), 2)
		write(t, filepath.Join(root, dir, "node_modules", "dep.bin"), trashSize)
	}

	if report := analyze(t, root, rules.Env{}); len(report.Findings) != 0 {
		t.Errorf("Findings = %+v", report.Findings)
	}
}

func TestNestedGeneratedFoldersCountOnce(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "package.json"), 2)
	write(t, filepath.Join(root, "node_modules", "dep", "package.json"), 2)
	write(t, filepath.Join(root, "node_modules", "dep", "node_modules", "sub.bin"), trashSize)

	finding := only(t, analyze(t, root, rules.Env{}))
	if finding.Path != filepath.Join(root, "node_modules") {
		t.Errorf("Path = %s", finding.Path)
	}
}

func TestSkipsTinyFolders(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "package.json"), 2)
	write(t, filepath.Join(root, "node_modules", "dep.bin"), 4096)

	if report := analyze(t, root, rules.Env{}); len(report.Findings) != 0 {
		t.Errorf("Findings = %+v", report.Findings)
	}
}

func TestPythonVenvUsesInsideMarker(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "uno", "venv", "pyvenv.cfg"), 2)
	write(t, filepath.Join(root, "uno", "venv", "lib.bin"), trashSize)
	write(t, filepath.Join(root, "uno", "requirements.txt"), 2)
	write(t, filepath.Join(root, "dos", "venv", "lib.bin"), trashSize)

	finding := only(t, analyze(t, root, rules.Env{}))
	if finding.Project != filepath.Join(root, "uno") || finding.Advice.Level != rules.LevelSafe {
		t.Errorf("Finding = %+v", finding)
	}
}

func TestDotNetMarkerPattern(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "App.csproj"), 2)
	write(t, filepath.Join(root, "obj", "x.bin"), trashSize)

	if finding := only(t, analyze(t, root, rules.Env{})); finding.Kind != "obj" {
		t.Errorf("Kind = %s", finding.Kind)
	}
}

func TestLastActivityIgnoresGeneratedFolder(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "package.json")
	write(t, source, 2)
	write(t, filepath.Join(root, "node_modules", "dep.bin"), trashSize)
	old := time.Date(2021, 3, 4, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}

	if got := only(t, analyze(t, root, rules.Env{})).LastActivity; got != old.Unix() {
		t.Errorf("LastActivity = %d, quiero %d", got, old.Unix())
	}
}

func TestFindsCaches(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "AppData", "Local")
	write(t, filepath.Join(local, "npm-cache", "a.bin"), trashSize)
	write(t, filepath.Join(local, "Docker", "disk.vhdx"), trashSize)

	report := analyze(t, root, rules.Env{Home: root, LocalAppData: local})
	levels := map[string]rules.Level{}
	for _, finding := range report.Findings {
		if finding.Category != CategoryCache {
			t.Errorf("Category = %s", finding.Category)
		}
		levels[finding.Kind] = finding.Advice.Level
	}
	if levels["npm"] != rules.LevelSafe || levels["Docker"] != rules.LevelReview {
		t.Errorf("levels = %v", levels)
	}
}

func TestEveryRuleHasBothLanguages(t *testing.T) {
	for _, rule := range projectRules {
		if rule.what.ES == "" || rule.what.EN == "" {
			t.Errorf("%s: falta idioma", rule.name)
		}
		if len(rule.markers) == 0 && rule.insideMarker == "" {
			t.Errorf("%s: regla sin marcador", rule.name)
		}
	}
}

// TestRealAnalyze corre contra el disco real con EL_BUSCADOR_REAL_SCAN. Solo lee.
func TestRealAnalyze(t *testing.T) {
	root := os.Getenv("EL_BUSCADOR_REAL_SCAN")
	if root == "" {
		t.Skip("define EL_BUSCADOR_REAL_SCAN con una ruta")
	}
	started := time.Now()
	result, err := scanner.Scan(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	scanned := time.Since(started)
	report, err := Analyze(context.Background(), result, rules.New(rules.EnvFromOS()))
	if err != nil {
		t.Fatal(err)
	}
	const gib = 1024 * 1024 * 1024
	t.Logf("escaneo %.1f s, análisis %.1f s, %d hallazgos, seguro %.2f GB", scanned.Seconds(), (time.Since(started) - scanned).Seconds(), len(report.Findings), float64(report.SafeSize)/gib)
	for _, finding := range report.Findings[:min(40, len(report.Findings))] {
		t.Logf("%-6s %7.2f GB %-8s %s", finding.Advice.Level, float64(finding.Size)/gib, finding.Category, finding.Path)
	}
	for _, finding := range report.Findings {
		if strings.Contains(strings.ToLower(finding.Path), `staytrue\`) {
			t.Logf("staytrue: %s %s: %s", finding.Advice.Level, finding.Path, finding.Advice.Reason.ES)
		}
	}
}
