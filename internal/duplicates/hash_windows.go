package duplicates

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/windows"
	"lukechampine.com/blake3"
)

const (
	partialChunk = 64 * 1024
	readBuffer   = 1024 * 1024
	hashBytes    = 32

	// Pide leer sin traer el archivo desde almacenamiento remoto.
	fileFlagOpenNoRecall = 0x00100000

	cloudOnlyAttrs = windows.FILE_ATTRIBUTE_RECALL_ON_OPEN |
		windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS |
		windows.FILE_ATTRIBUTE_OFFLINE

	reparseTagCloudMask = 0xFFFF0FFF
	reparseTagCloud     = 0x9000001A
	reparseTagOneDrive  = 0x80000021
)

// ErrCloudOnly significa que leer el archivo dispararía una descarga.
var ErrCloudOnly = errors.New("archivo solo en la nube")

// ErrLink significa que la ruta apunta a otro lugar.
var ErrLink = errors.New("es un enlace")

// DiskReader lee del disco real.
type DiskReader struct{}

func longPath(path string) string {
	if strings.HasPrefix(path, `\\`) {
		return path
	}
	return `\\?\` + path
}

// checkReadable solo consulta atributos, así que no descarga nada.
func checkReadable(path string) error {
	wide, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return err
	}
	var data windows.Win32finddata
	handle, err := windows.FindFirstFile(wide, &data)
	if err != nil {
		return fmt.Errorf("leer %q: %w", path, err)
	}
	windows.FindClose(handle)

	if data.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return ErrLink
	}
	if data.FileAttributes&cloudOnlyAttrs != 0 {
		return ErrCloudOnly
	}
	if data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		tag := data.Reserved0
		if tag&reparseTagCloudMask != reparseTagCloud && tag != reparseTagOneDrive {
			return ErrLink
		}
	}
	return nil
}

// openForRead abre sin bloquear a otros programas, sin seguir enlaces y sin traer
// nada de la nube. Revisa atributos antes y después de abrir: el archivo pudo cambiar
// de estado desde el escaneo.
func openForRead(path string) (*os.File, windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	if err := checkReadable(path); err != nil {
		return nil, info, err
	}
	wide, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return nil, info, err
	}
	handle, err := windows.CreateFile(wide, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_SEQUENTIAL_SCAN|windows.FILE_FLAG_OPEN_REPARSE_POINT|fileFlagOpenNoRecall, 0)
	if err != nil {
		return nil, info, fmt.Errorf("abrir %q: %w", path, err)
	}
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		windows.CloseHandle(handle)
		return nil, info, fmt.Errorf("leer %q: %w", path, err)
	}
	if info.FileAttributes&cloudOnlyAttrs != 0 {
		windows.CloseHandle(handle)
		return nil, info, ErrCloudOnly
	}
	return os.NewFile(uintptr(handle), path), info, nil
}

func sizeOf(info windows.ByHandleFileInformation) int64 {
	return int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow)
}

func newHash() hash.Hash { return blake3.New(hashBytes, nil) }

func sumHex(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

// Partial lee el principio y el final. Dos archivos distintos suelen diferir ahí, y
// así se evita leerlos completos.
func (DiskReader) Partial(ctx context.Context, file File) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, info, err := openForRead(file.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	size := sizeOf(info)
	if size != file.Size {
		return "", fmt.Errorf("%q cambió de tamaño", file.Path)
	}
	h := newHash()
	if size <= 2*partialChunk {
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
		return sumHex(h), nil
	}
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, partialChunk)); err != nil {
		return "", err
	}
	if _, err := io.Copy(h, io.NewSectionReader(f, size-partialChunk, partialChunk)); err != nil {
		return "", err
	}
	return sumHex(h), nil
}

// Full lee todo el contenido, y se puede cancelar entre bloques.
func (DiskReader) Full(ctx context.Context, file File, onBytes func(int64)) (string, error) {
	f, info, err := openForRead(file.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if sizeOf(info) != file.Size {
		return "", fmt.Errorf("%q cambió de tamaño", file.Path)
	}
	return hashStream(ctx, f, onBytes)
}

func hashStream(ctx context.Context, r io.Reader, onBytes func(int64)) (string, error) {
	h := newHash()
	buffer := make([]byte, readBuffer)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := r.Read(buffer)
		if n > 0 {
			h.Write(buffer[:n])
			if onBytes != nil {
				onBytes(int64(n))
			}
		}
		if errors.Is(err, io.EOF) {
			return sumHex(h), nil
		}
		if err != nil {
			return "", err
		}
	}
}
