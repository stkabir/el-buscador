package actions

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	bitBucketKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\`
	mb           = 1024 * 1024
	// Sin ajuste guardado se asume un 5% del disco, para errar del lado cauto.
	defaultBinPercent = 5
)

// BinSpace describe la papelera de un disco. Room es lo que cabe sin que Windows
// descarte, de forma permanente, lo más viejo que ya tiene.
type BinSpace struct {
	Capacity int64 `json:"capacity"`
	Used     int64 `json:"used"`
	Room     int64 `json:"room"`
	Known    bool  `json:"known"`
}

// binSettings lee si la papelera está desactivada y su capacidad.
func binSettings(path string) (off bool, capacity int64, err error) {
	wide, err := windows.UTF16PtrFromString(filepath.VolumeName(path) + `\`)
	if err != nil {
		return false, 0, err
	}
	var free, total uint64
	if err := windows.GetDiskFreeSpaceEx(wide, &free, &total, nil); err != nil {
		return false, 0, fmt.Errorf("leer disco: %w", err)
	}
	capacity = int64(total / 100 * defaultBinPercent)

	buffer := make([]uint16, 64)
	if err := windows.GetVolumeNameForVolumeMountPoint(wide, &buffer[0], uint32(len(buffer))); err != nil {
		return false, capacity, nil
	}
	volume := strings.TrimSuffix(strings.TrimPrefix(windows.UTF16ToString(buffer), `\\?\Volume`), `\`)
	key, err := registry.OpenKey(registry.CURRENT_USER, bitBucketKey+volume, registry.QUERY_VALUE)
	if err != nil {
		return false, capacity, nil
	}
	defer key.Close()

	if nuke, _, err := key.GetIntegerValue("NukeOnDelete"); err == nil {
		off = nuke != 0
	}
	if maxMB, _, err := key.GetIntegerValue("MaxCapacity"); err == nil {
		capacity = int64(maxMB) * mb
	}
	return off, capacity, nil
}

// checkRecycleBin revisa si la papelera del disco aceptaría size bytes. Es solo un
// aviso temprano: la garantía final es el veto de MoveToTrash.
func checkRecycleBin(path string, size int64) error {
	off, capacity, err := binSettings(path)
	if err != nil {
		return err
	}
	return binAllows(off, capacity, size)
}

// shQueryRBInfo refleja SHQUERYRBINFO en 64 bits.
type shQueryRBInfo struct {
	size     uint32
	_        uint32
	binBytes int64
	binItems int64
}

// RecycleBinSpace mide la papelera del disco donde está path.
func RecycleBinSpace(path string) (BinSpace, error) {
	off, capacity, err := binSettings(path)
	if err != nil {
		return BinSpace{}, err
	}
	root, err := windows.UTF16PtrFromString(filepath.VolumeName(path) + `\`)
	if err != nil {
		return BinSpace{}, err
	}
	info := shQueryRBInfo{size: uint32(unsafe.Sizeof(shQueryRBInfo{}))}
	r, _, _ := procSHQueryRecycleBin.Call(uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&info)))
	if failed(uint32(r)) {
		return BinSpace{}, fmt.Errorf("leer la papelera: 0x%08X", uint32(r))
	}
	space := BinSpace{Capacity: capacity, Used: info.binBytes, Known: true}
	if !off {
		space.Room = max(capacity-info.binBytes, 0)
	}
	return space, nil
}

func binAllows(off bool, capacity, size int64) error {
	switch {
	case off:
		return refuse(CodeBinOff)
	case size >= capacity:
		return refuse(CodeTooBig)
	}
	return nil
}

// measure suma lo que hay en path sin seguir enlaces y sin abrir archivos.
func measure(ctx context.Context, path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil // lo ilegible no cambia la decisión: el veto sigue activo
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	if errors.Is(err, context.Canceled) {
		return 0, err
	}
	return total, nil
}
