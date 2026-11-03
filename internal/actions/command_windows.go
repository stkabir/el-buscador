package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func hiddenWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// ToolInstalled indica si el programa está en el PATH.
func ToolInstalled(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// RunCommand ejecuta un comando oficial sin shell y manda su salida a output. Corre
// en dir para no leer la configuración de ningún proyecto.
func RunCommand(ctx context.Context, argv []string, dir string, output io.Writer) error {
	if len(argv) == 0 {
		return refuse(CodeFailed)
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return refuse(CodeToolMissing)
	}

	cmd := exec.CommandContext(ctx, path, argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = hiddenWindow()
	// npm y similares son .cmd: matar solo ese proceso dejaría vivo a node.
	cmd.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.SysProcAttr = hiddenWindow()
		return kill.Run()
	}
	cmd.WaitDelay = 5 * time.Second

	err = cmd.Run()
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return refuse(CodeCanceled)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		fmt.Fprintf(output, "\nexit %d\n", exit.ExitCode())
		return refuse(CodeToolFailed)
	}
	return err
}

// Reveal abre el Explorador con path seleccionado. Solo lee.
func Reveal(path string) error {
	if !isPlainDrivePath(path) {
		return refuse(CodeInvalidPath)
	}
	if _, err := inspect(path); err != nil {
		return err
	}
	cmd := exec.Command("explorer.exe")
	// Explorer no entiende las comillas que genera Go alrededor del argumento.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: revealCmdLine(path)}
	return ignoreExit(cmd.Run())
}

func revealCmdLine(path string) string {
	return `explorer.exe /select,"` + path + `"`
}

// OpenRecycleBin abre la papelera. Nunca la vacía.
func OpenRecycleBin() error {
	return ignoreExit(exec.Command("explorer.exe", "shell:RecycleBinFolder").Run())
}

// ignoreExit existe porque Explorer devuelve 1 aunque funcione.
func ignoreExit(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return err
}
