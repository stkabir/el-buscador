package devtrash

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

type gitState int

const (
	gitNoRepo gitState = iota
	gitTracked
	gitIgnored
	gitUntracked
	// gitUnknown significa que git falló. Se trata como riesgo, nunca como seguro.
	gitUnknown
)

// readGitState indica qué piensa git de una carpeta dentro de project.
func readGitState(ctx context.Context, project, name string) gitState {
	root := findRepoRoot(project)
	if root == "" {
		return gitNoRepo
	}
	rel, err := filepath.Rel(root, filepath.Join(project, name))
	if err != nil {
		return gitUnknown
	}
	rel = filepath.ToSlash(rel)

	tracked, err := runGit(ctx, root, "ls-files", "-z", "--", ":(literal)"+rel).Output()
	if err != nil {
		return gitUnknown
	}
	if len(tracked) > 0 {
		return gitTracked
	}

	err = runGit(ctx, root, "check-ignore", "-q", "--", rel+"/").Run()
	if err == nil {
		return gitIgnored
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return gitUntracked
	}
	return gitUnknown
}

// GitTracksFile indica si git rastrea un archivo. Si git falla responde true: lo
// rastreado nunca es seguro, y no se pudo descartar.
func GitTracksFile(ctx context.Context, path string) bool {
	root := findRepoRoot(filepath.Dir(path))
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	tracked, err := runGit(ctx, root, "ls-files", "-z", "--", ":(literal)"+filepath.ToSlash(rel)).Output()
	return err != nil || len(tracked) > 0
}

func findRepoRoot(dir string) string {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// runGit prepara un comando que no modifica el repo. Estos repos no son de
// confianza: core.fsmonitor en su configuración podría ejecutar programas.
func runGit(ctx context.Context, root string, args ...string) *exec.Cmd {
	full := append([]string{"-c", "core.fsmonitor=false", "-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	hideWindow(cmd)
	return cmd
}
