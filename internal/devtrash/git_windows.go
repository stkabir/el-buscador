package devtrash

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideWindow evita que aparezca una consola por cada llamada a git.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
