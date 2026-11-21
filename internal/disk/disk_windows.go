// Package disk lee el espacio de los discos del sistema.
package disk

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"golang.org/x/sys/windows"
)

// Usage describe el espacio de un disco, en bytes.
type Usage struct {
	Drive string `json:"drive"`
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
	// System indica el disco donde está instalado Windows.
	System bool `json:"system"`
}

func systemDrive() string {
	if drive := os.Getenv("SystemDrive"); drive != "" {
		return drive
	}
	return "C:"
}

// System devuelve el espacio del disco donde está instalado Windows.
func System() (Usage, error) { return Of(systemDrive()) }

// Of devuelve el espacio de un disco, como "D:".
func Of(drive string) (Usage, error) {
	path, err := windows.UTF16PtrFromString(drive + `\`)
	if err != nil {
		return Usage{}, fmt.Errorf("ruta de disco %q: %w", drive, err)
	}

	var free, total uint64
	if err := windows.GetDiskFreeSpaceEx(path, &free, &total, nil); err != nil {
		return Usage{}, fmt.Errorf("leer disco %q: %w", drive, err)
	}

	return Usage{Drive: drive, Total: total, Free: free, System: strings.EqualFold(drive, systemDrive())}, nil
}

// Fixed lista los discos fijos, con el del sistema primero. Deja fuera los de red y
// los extraíbles: no tienen papelera confiable y se pueden ir a medio escaneo.
func Fixed() ([]Usage, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, fmt.Errorf("listar discos: %w", err)
	}
	drives := []Usage{}
	for i := range 26 {
		if mask&(1<<i) == 0 {
			continue
		}
		drive := string(rune('A'+i)) + ":"
		root, err := windows.UTF16PtrFromString(drive + `\`)
		if err != nil || windows.GetDriveType(root) != windows.DRIVE_FIXED {
			continue
		}
		// Un disco fijo sin montar no es motivo para fallar: se omite.
		if usage, err := Of(drive); err == nil {
			drives = append(drives, usage)
		}
	}
	slices.SortStableFunc(drives, func(a, b Usage) int {
		switch {
		case a.System && !b.System:
			return -1
		case b.System && !a.System:
			return 1
		}
		return 0
	})
	return drives, nil
}

// IsFixed indica si drive, como "D:", es un disco fijo de este equipo.
func IsFixed(drive string) bool {
	drives, err := Fixed()
	if err != nil {
		return false
	}
	return slices.ContainsFunc(drives, func(usage Usage) bool { return strings.EqualFold(usage.Drive, drive) })
}
