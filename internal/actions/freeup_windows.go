package actions

import (
	"context"
	"encoding/binary"
	"errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	attrPinned   = 0x00080000
	attrUnpinned = 0x00100000

	cloudOnlyAttrs = windows.FILE_ATTRIBUTE_RECALL_ON_OPEN |
		windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS |
		windows.FILE_ATTRIBUTE_OFFLINE
)

// CodeNotSynced: la ruta no está en una carpeta que se sincronice con la nube.
const CodeNotSynced Code = "notSynced"

// FreeUpResult dice qué pasó al liberar espacio. Requested es lo que se le pidió al
// programa de la nube: lo saca del disco en segundo plano, y puede negarse con lo que
// aún no termina de subir.
type FreeUpResult struct {
	Files     int64 `json:"files"`
	Requested int64 `json:"requested"`
	// Already ya estaba solo en la nube.
	Already int64 `json:"already"`
	// Pending aún no termina de subir: se queda en el disco.
	Pending int64 `json:"pending"`
	// Local no está administrado por la nube, así que no se tocó.
	Local  int64 `json:"local"`
	Failed int64 `json:"failed"`
	// Canceled indica que se detuvo: lo pedido hasta ahí sigue pedido.
	Canceled bool `json:"canceled"`
	// Code no vacío dice por qué no se pudo ni empezar.
	Code Code `json:"code"`
}

// cloudFile es lo que hace falta saber de una ruta para decidir.
type cloudFile struct {
	attrs uint32
	tag   uint32
}

func (f cloudFile) managed() bool {
	return f.attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 &&
		(f.tag&reparseTagCloudMask == reparseTagCloud || f.tag == reparseTagOneDrive)
}

// freeUpFS es el acceso al disco de FreeUpSpace. Se reemplaza en tests, porque un
// archivo de nube real requiere un proveedor instalado.
type freeUpFS struct {
	read func(path string) (cloudFile, error)
	// inSync dice si ya terminó de subir. Ante la duda responde false.
	inSync func(path string) bool
	// write nil solo revisa y no cambia nada.
	write func(path string, attrs uint32) error
}

func readCloudFile(path string) (cloudFile, error) {
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return cloudFile{}, err
	}
	var data windows.Win32finddata
	handle, err := windows.FindFirstFile(wide, &data)
	if err != nil {
		return cloudFile{}, err
	}
	windows.FindClose(handle)
	return cloudFile{attrs: data.FileAttributes, tag: data.Reserved0}, nil
}

func writeAttrs(path string, attrs uint32) error {
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.SetFileAttributes(wide, attrs)
}

var procCfGetPlaceholderInfo = windows.NewLazySystemDLL("cldapi.dll").NewProc("CfGetPlaceholderInfo")

const (
	cfPlaceholderInfoBasic = 0
	cfInSync               = 1
	// CF_PLACEHOLDER_BASIC_INFO: PinState (4 bytes), InSyncState (4 bytes), y una
	// identidad de hasta 4 KB que aquí no se usa.
	cfInfoBuffer = 4096 + 32
)

// placeholderInSync pregunta a Windows si el archivo ya subió. Abre solo atributos: no
// lee contenido, así que no lo descarga. Ante cualquier error responde false, y el
// archivo se queda en el disco.
func placeholderInSync(path string) bool {
	if procCfGetPlaceholderInfo.Find() != nil {
		return false
	}
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	handle, err := windows.CreateFile(wide, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	buffer := make([]byte, cfInfoBuffer)
	r, _, _ := procCfGetPlaceholderInfo.Call(uintptr(handle), cfPlaceholderInfoBasic,
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
	if failed(uint32(r)) {
		return false
	}
	return binary.LittleEndian.Uint32(buffer[4:8]) == cfInSync
}

// CheckFreeUp valida la ruta. No basta con que esté en una carpeta sincronizada: un
// enlace podría llevar a otro lugar.
func CheckFreeUp(path string, isSynced func(string) bool) error {
	if !isPlainDrivePath(path) {
		return refuse(CodeInvalidPath)
	}
	if !isSynced(path) {
		return refuse(CodeNotSynced)
	}
	info, err := inspect(path)
	if err != nil {
		return err
	}
	if info.link {
		return refuse(CodeLink)
	}
	final, err := finalPath(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(final, path) {
		return refuse(CodeLink)
	}
	return nil
}

// FreeUpSpace pide sacar del disco lo que hay en path, dejándolo en la nube. Es lo que
// hace "Liberar espacio" en el Explorador: marca los archivos y el programa de la nube
// hace el resto. No borra nada, y no abre ni descarga archivos.
func FreeUpSpace(ctx context.Context, path string) FreeUpResult {
	return freeUp(ctx, path, freeUpFS{read: readCloudFile, inSync: placeholderInSync, write: writeAttrs})
}

// SurveyFreeUp cuenta lo que FreeUpSpace pediría, sin cambiar nada.
func SurveyFreeUp(ctx context.Context, path string) FreeUpResult {
	return freeUp(ctx, path, freeUpFS{read: readCloudFile, inSync: placeholderInSync})
}

// placeholderExpose es PHCM_EXPOSE_PLACEHOLDERS. Por omisión Windows les esconde la
// marca de nube a los programas: FindFirstFile los muestra como archivos normales, y
// sin esto nada se reconocería como administrado por la nube.
const placeholderExpose = 2

var procSetThreadPlaceholderMode = windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlSetThreadPlaceholderCompatibilityMode")

// exposePlaceholders aplica solo a este hilo, para no cambiar cómo ve el disco el resto
// de la app. Si no se puede, los archivos se ven como locales y no se toca nada.
func exposePlaceholders() (restore func()) {
	runtime.LockOSThread()
	if procSetThreadPlaceholderMode.Find() != nil {
		return runtime.UnlockOSThread
	}
	previous, _, _ := procSetThreadPlaceholderMode.Call(placeholderExpose)
	return func() {
		procSetThreadPlaceholderMode.Call(previous)
		runtime.UnlockOSThread()
	}
}

func freeUp(ctx context.Context, root string, disk freeUpFS) FreeUpResult {
	defer exposePlaceholders()()

	var result FreeUpResult
	// WalkDir no entra en enlaces ni junctions: quedan como entradas que no son carpeta.
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			result.Failed++
			return nil
		}
		if entry.IsDir() {
			// Marcar la carpeta le dice a la nube que lo nuevo tampoco se quede. Lo que
			// está dentro y no ha subido se protege uno por uno, abajo.
			unpin(path, disk)
			return nil
		}
		if !entry.Type().IsRegular() && entry.Type()&fs.ModeIrregular == 0 {
			result.Local++
			return nil
		}

		file, err := disk.read(path)
		switch {
		case err != nil:
			result.Failed++
		case !file.managed():
			result.Local++
		case file.attrs&cloudOnlyAttrs != 0:
			result.Already++
		case !disk.inSync(path):
			result.Pending++
		default:
			if !unpin(path, disk) {
				result.Failed++
				return nil
			}
			result.Files++
			if info, err := entry.Info(); err == nil {
				result.Requested += info.Size()
			}
		}
		return nil
	})
	result.Canceled = errors.Is(err, context.Canceled)
	return result
}

// unpin quita "mantener siempre en este dispositivo" y pide liberarlo. Solo actúa en
// rutas administradas por la nube.
func unpin(path string, disk freeUpFS) bool {
	file, err := disk.read(path)
	if err != nil || !file.managed() {
		return false
	}
	if disk.write == nil {
		return true
	}
	wanted := (file.attrs &^ attrPinned) | attrUnpinned
	if wanted == file.attrs {
		return true
	}
	return disk.write(path, wanted) == nil
}
