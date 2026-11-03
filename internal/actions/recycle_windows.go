package actions

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	bitBucketKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\`
	mb           = 1024 * 1024
	// Sin ajuste guardado se asume un 5% del disco, para errar del lado cauto.
	defaultBinPercent = 5
)

// checkRecycleBin revisa si la papelera del disco aceptaría size bytes. Es solo un
// aviso temprano: la garantía final es el veto de MoveToTrash.
func checkRecycleBin(path string, size int64) error {
	root := filepath.VolumeName(path) + `\`
	wide, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return err
	}
	var free, total uint64
	if err := windows.GetDiskFreeSpaceEx(wide, &free, &total, nil); err != nil {
		return fmt.Errorf("leer disco: %w", err)
	}
	capacity := int64(total / 100 * defaultBinPercent)

	buffer := make([]uint16, 64)
	if err := windows.GetVolumeNameForVolumeMountPoint(wide, &buffer[0], uint32(len(buffer))); err != nil {
		return binAllows(false, capacity, size)
	}
	volume := strings.TrimSuffix(strings.TrimPrefix(windows.UTF16ToString(buffer), `\\?\Volume`), `\`)
	key, err := registry.OpenKey(registry.CURRENT_USER, bitBucketKey+volume, registry.QUERY_VALUE)
	if err != nil {
		return binAllows(false, capacity, size)
	}
	defer key.Close()

	nuke, _, err := key.GetIntegerValue("NukeOnDelete")
	if err != nil {
		nuke = 0
	}
	if maxMB, _, err := key.GetIntegerValue("MaxCapacity"); err == nil {
		capacity = int64(maxMB) * mb
	}
	return binAllows(nuke != 0, capacity, size)
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
