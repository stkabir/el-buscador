package devtrash

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

const cloudOnlyAttrs = windows.FILE_ATTRIBUTE_RECALL_ON_OPEN |
	windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS |
	windows.FILE_ATTRIBUTE_OFFLINE

// isCloudOnly indica si abrir el archivo dispararía una descarga. Ante la duda
// responde true.
func isCloudOnly(path string) bool {
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attrs, err := windows.GetFileAttributes(wide)
	return err != nil || attrs&cloudOnlyAttrs != 0
}

// hideWindow evita que aparezca una consola por cada llamada a git.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
