// Package disk lee el espacio de los discos del sistema.
package disk

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Usage describe el espacio de un disco, en bytes.
type Usage struct {
	Drive string `json:"drive"`
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
}

// System devuelve el espacio del disco donde está instalado Windows.
func System() (Usage, error) {
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = "C:"
	}

	path, err := windows.UTF16PtrFromString(drive + `\`)
	if err != nil {
		return Usage{}, fmt.Errorf("ruta de disco %q: %w", drive, err)
	}

	var free, total uint64
	if err := windows.GetDiskFreeSpaceEx(path, &free, &total, nil); err != nil {
		return Usage{}, fmt.Errorf("leer disco %q: %w", drive, err)
	}

	return Usage{Drive: drive, Total: total, Free: free}, nil
}
