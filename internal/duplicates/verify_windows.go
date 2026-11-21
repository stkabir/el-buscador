package duplicates

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/sys/windows"

	"el-buscador/internal/actions"
)

// VerifyCopy comprueba justo antes de borrar que copy sea una copia real de keep y
// que keep siga intacta. Lee ambos completos y nunca usa la caché: es la última
// defensa para no dejar al usuario sin ninguna copia.
func VerifyCopy(ctx context.Context, keep, copyPath, wantHash string, size int64) error {
	if strings.EqualFold(keep, copyPath) {
		return actions.Refuse(actions.CodeNoCopyLeft)
	}
	// La copia que se conserva no puede ser una que ya está en la papelera.
	if strings.Contains(strings.ToLower(keep), `\$recycle.bin\`) {
		return actions.Refuse(actions.CodeNoCopyLeft)
	}

	keepHash, keepInfo, err := hashForVerify(ctx, keep, size)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return actions.Refuse(actions.CodeCanceled)
		}
		return actions.Refuse(actions.CodeNoCopyLeft)
	}
	if keepHash != wantHash {
		return actions.Refuse(actions.CodeNoCopyLeft)
	}

	copyHash, copyInfo, err := hashForVerify(ctx, copyPath, size)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return actions.Refuse(actions.CodeCanceled)
		}
		return actions.Refuse(actions.CodeNotDuplicate)
	}
	if copyHash != wantHash {
		return actions.Refuse(actions.CodeNotDuplicate)
	}
	// Dos nombres del mismo archivo (hard link): borrar uno no libera nada, y no es
	// una copia independiente.
	if sameFile(keepInfo, copyInfo) {
		return actions.Refuse(actions.CodeNotDuplicate)
	}
	return nil
}

func hashForVerify(ctx context.Context, path string, size int64) (string, windows.ByHandleFileInformation, error) {
	f, info, err := openForRead(path)
	if err != nil {
		return "", info, err
	}
	defer f.Close()
	if sizeOf(info) != size {
		return "", info, errors.New("cambió de tamaño")
	}
	hash, err := hashStream(ctx, f, nil)
	return hash, info, err
}

func sameFile(a, b windows.ByHandleFileInformation) bool {
	return a.VolumeSerialNumber == b.VolumeSerialNumber &&
		a.FileIndexHigh == b.FileIndexHigh && a.FileIndexLow == b.FileIndexLow
}
