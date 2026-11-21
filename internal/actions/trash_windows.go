package actions

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrWouldDeletePermanently significa que Windows no podía usar la papelera y que
// se detuvo la operación antes de borrar.
var ErrWouldDeletePermanently = errors.New("la papelera no acepta este elemento")

// ErrInUse significa que otro programa tiene abierto el elemento.
var ErrInUse = errors.New("elemento en uso")

// ErrCanceled significa que la operación se canceló antes de tocar el elemento.
var ErrCanceled = errors.New("cancelado")

const (
	fofSilent          = 0x0004
	fofNoConfirmation  = 0x0010
	fofAllowUndo       = 0x0040
	fofNoConfirmMkdir  = 0x0200
	fofNoErrorUI       = 0x0400
	fofxRecycleOnDelet = 0x00080000

	// Ninguna opción muestra ventanas. Sin FOF_WANTNUKEWARNING, si la papelera rechaza
	// algo Windows reintenta sin ella y pasa por sinkPreDelete, que lo veta. Con esa
	// opción, en cambio, se queda esperando un clic y nunca llega al sink.
	trashFlags = fofSilent | fofNoConfirmation | fofAllowUndo | fofNoConfirmMkdir |
		fofNoErrorUI | fofxRecycleOnDelet

	tsfDeleteRecycleIfPossible = 0x80
	sigdnFileSysPath           = 0x80058000

	clsctxAll    = 0x17
	sOK          = 0
	eNoInterface = 0x80004002
	hrCanceled   = 0x800704C7
	hrAccess     = 0x80070005
	hrSharing    = 0x80070020
	hrLock       = 0x80070021

	vtSetOperationFlags       = 5
	vtAdvise                  = 3
	vtUnadvise                = 4
	vtDeleteItem              = 18
	vtPerformOperations       = 21
	vtGetAnyOperationsAborted = 22
	vtRelease                 = 2
	vtGetDisplayName          = 5
)

var (
	clsidFileOperation = windows.GUID{Data1: 0x3AD05575, Data2: 0x8857, Data3: 0x4850, Data4: [8]byte{0x92, 0x77, 0x11, 0xB8, 0x5B, 0xDB, 0x8E, 0x09}}
	iidFileOperation   = windows.GUID{Data1: 0x947AAB5F, Data2: 0x0A5C, Data3: 0x4C13, Data4: [8]byte{0xB4, 0xD6, 0x4B, 0xF7, 0x83, 0x6F, 0xC9, 0xF8}}
	iidShellItem       = windows.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
	iidProgressSink    = windows.GUID{Data1: 0x04B0F1A7, Data2: 0x9490, Data3: 0x44BC, Data4: [8]byte{0x96, 0xE1, 0x42, 0x96, 0xA3, 0x12, 0x52, 0xE2}}
	iidUnknown         = windows.GUID{Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}

	ole32                       = windows.NewLazySystemDLL("ole32.dll")
	shell32                     = windows.NewLazySystemDLL("shell32.dll")
	procCoCreateInstance        = ole32.NewProc("CoCreateInstance")
	procCreateItemFromParseName = shell32.NewProc("SHCreateItemFromParsingName")
	procSHQueryRecycleBin       = shell32.NewProc("SHQueryRecycleBinW")
)

// comCall llama al método index de la tabla virtual de un objeto COM.
func comCall(object uintptr, index int, args ...uintptr) uint32 {
	vtable := *(*uintptr)(unsafe.Pointer(object))
	method := *(*uintptr)(unsafe.Pointer(vtable + uintptr(index)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(method, append([]uintptr{object}, args...)...)
	return uint32(r)
}

func failed(hr uint32) bool { return int32(hr) < 0 }

// sinkState es lo que el sink observa de la operación en curso. Solo hay una a la
// vez (trashMu) y Windows llama al sink desde el mismo hilo que la inició.
type sinkState struct {
	canceled   func() bool
	vetoed     bool
	stopped    bool
	recycledAs string
	deleteHR   uint32
}

var (
	trashMu sync.Mutex
	sink    *sinkState
	// comOut recibe lo que escribe COM. Es global porque comCall recibe direcciones
	// como uintptr: una variable local vive en la pila, Go puede moverla y Windows
	// escribiría en memoria vieja. trashMu protege su uso.
	comOut struct {
		operation uintptr
		item      uintptr
		cookie    uintptr
		name      *uint16
	}
)

func sinkNoop(this uintptr) uintptr { return sOK }
func sinkRef(this uintptr) uintptr  { return 1 }

func sinkQueryInterface(this, riid, out uintptr) uintptr {
	id := (*windows.GUID)(unsafe.Pointer(riid))
	if *id != iidProgressSink && *id != iidUnknown {
		*(*uintptr)(unsafe.Pointer(out)) = 0
		return eNoInterface
	}
	*(*uintptr)(unsafe.Pointer(out)) = this
	return sOK
}

// sinkPreDelete es el punto de control: se ejecuta antes de tocar cada elemento y
// devolver un error aquí impide el borrado.
func sinkPreDelete(this, flags, item uintptr) uintptr {
	if sink.canceled() {
		sink.stopped = true
		return hrCanceled
	}
	if flags&tsfDeleteRecycleIfPossible == 0 {
		sink.vetoed = true
		return hrCanceled
	}
	return sOK
}

func sinkPostDelete(this, flags, item, hr, created uintptr) uintptr {
	result := uint32(hr)
	if failed(result) {
		sink.deleteHR = result
		return sOK
	}
	// Sin copia en la papelera, Windows todavía puede reintentar sin ella. Qué pasó
	// al final lo decide MoveToTrash mirando el disco.
	if created == 0 {
		return sOK
	}
	if failed(comCall(created, vtGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&comOut.name)))) {
		return sOK
	}
	sink.recycledAs = windows.UTF16PtrToString(comOut.name)
	windows.CoTaskMemFree(unsafe.Pointer(comOut.name))
	return sOK
}

// El orden es el de IFileOperationProgressSink. sinkNoop acepta menos argumentos
// de los que recibe porque en x64 y arm64 quien llama limpia la pila.
var sinkVtable = func() *[19]uintptr {
	noop := syscall.NewCallback(sinkNoop)
	ref := syscall.NewCallback(sinkRef)
	return &[19]uintptr{
		syscall.NewCallback(sinkQueryInterface), ref, ref,
		noop, noop, // StartOperations, FinishOperations
		noop, noop, // PreRenameItem, PostRenameItem
		noop, noop, // PreMoveItem, PostMoveItem
		noop, noop, // PreCopyItem, PostCopyItem
		syscall.NewCallback(sinkPreDelete), syscall.NewCallback(sinkPostDelete),
		noop, noop, // PreNewItem, PostNewItem
		noop, noop, noop, noop, // UpdateProgress, ResetTimer, PauseTimer, ResumeTimer
	}
}()

// sinkObject es global para que su dirección siga siendo válida mientras COM la use.
var sinkObject = struct{ vtable *[19]uintptr }{sinkVtable}

// MoveToTrash manda path a la papelera y devuelve dónde quedó. Nunca borra de forma
// permanente: si Windows no puede usar la papelera, devuelve
// ErrWouldDeletePermanently y el elemento sigue en su lugar.
func MoveToTrash(path string, canceled func() bool) (string, error) {
	trashMu.Lock()
	defer trashMu.Unlock()

	// COM exige que toda la operación ocurra en un mismo hilo.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil && !errors.Is(err, syscall.Errno(1)) {
		return "", fmt.Errorf("iniciar COM: %w", err)
	}
	defer windows.CoUninitialize()

	state := &sinkState{canceled: canceled}
	sink = state
	defer func() { sink = nil }()

	hr, err := performDelete(path)
	if err != nil {
		return "", err
	}

	_, statErr := os.Lstat(path)
	stillThere := statErr == nil
	switch {
	case state.recycledAs != "" && !stillThere:
		return state.recycledAs, nil
	case !stillThere:
		// El veto debe impedirlo. Si llega a pasar, se reporta tal cual.
		return "", fmt.Errorf("%q ya no está y no quedó en la papelera", path)
	case state.vetoed:
		return "", ErrWouldDeletePermanently
	case state.stopped:
		return "", ErrCanceled
	}
	if state.deleteHR != 0 {
		hr = state.deleteHR
	}
	if failed(hr) {
		return "", deleteError(hr)
	}
	return "", fmt.Errorf("%q sigue en su lugar", path)
}

func performDelete(path string) (uint32, error) {
	r, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOperation)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidFileOperation)), uintptr(unsafe.Pointer(&comOut.operation)),
	)
	if failed(uint32(r)) {
		return 0, fmt.Errorf("crear IFileOperation: 0x%08X", uint32(r))
	}
	operation := comOut.operation
	defer comCall(operation, vtRelease)

	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	r, _, _ = procCreateItemFromParseName.Call(
		uintptr(unsafe.Pointer(wide)), 0,
		uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&comOut.item)),
	)
	if failed(uint32(r)) {
		return 0, fmt.Errorf("abrir %q: 0x%08X", path, uint32(r))
	}
	item := comOut.item
	defer comCall(item, vtRelease)

	if hr := comCall(operation, vtSetOperationFlags, trashFlags); failed(hr) {
		return 0, fmt.Errorf("configurar la operación: 0x%08X", hr)
	}
	if hr := comCall(operation, vtAdvise, uintptr(unsafe.Pointer(&sinkObject)), uintptr(unsafe.Pointer(&comOut.cookie))); failed(hr) {
		return 0, fmt.Errorf("registrar el sink: 0x%08X", hr)
	}
	cookie := comOut.cookie
	defer comCall(operation, vtUnadvise, cookie)

	if hr := comCall(operation, vtDeleteItem, item, 0); failed(hr) {
		return 0, fmt.Errorf("preparar el envío: 0x%08X", hr)
	}
	return comCall(operation, vtPerformOperations), nil
}

// Códigos COPYENGINE_E_* de sherrors.h.
func deleteError(hr uint32) error {
	switch hr {
	case hrSharing, hrLock, 0x80270027, 0x80270028:
		return ErrInUse
	case hrCanceled, 0x80270000, 0x80270001:
		return ErrCanceled
	case hrAccess, 0x80270002, 0x80270021, 0x80270022:
		return fmt.Errorf("sin permiso: %w", os.ErrPermission)
	case 0x80270035, 0x80270036, 0x80270037, 0x80270038, 0x8027003A:
		// La papelera no sirve: desactivada, sin espacio, ruta muy profunda o ausente.
		return fmt.Errorf("%w (0x%08X)", ErrWouldDeletePermanently, hr)
	}
	return fmt.Errorf("Windows respondió 0x%08X", hr)
}
