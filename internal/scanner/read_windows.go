package scanner

import (
	"errors"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type entryKind int

const (
	kindFile entryKind = iota
	kindDir
	kindLink
)

const (
	reparseTagCloudMask = 0xFFFF0FFF
	reparseTagCloud     = 0x9000001A
	reparseTagOneDrive  = 0x80000021

	cloudOnlyAttrs = windows.FILE_ATTRIBUTE_RECALL_ON_OPEN |
		windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS |
		windows.FILE_ATTRIBUTE_OFFLINE

	dirBufferWords = 8 * 1024 // 64 KB, alineados a 8 bytes como exige la API
)

// fileIDBothDirInfo refleja FILE_ID_BOTH_DIR_INFO. Si el archivo es un reparse
// point, eaSize trae su etiqueta en vez del tamaño de atributos extendidos.
type fileIDBothDirInfo struct {
	nextEntryOffset uint32
	fileIndex       uint32
	creationTime    int64
	lastAccessTime  int64
	lastWriteTime   int64
	changeTime      int64
	endOfFile       int64
	allocationSize  int64
	fileAttributes  uint32
	fileNameLength  uint32
	eaSize          uint32
	shortNameLength int8
	_               int8
	shortName       [12]uint16
	fileID          int64
	fileName        [1]uint16
}

type rawEntry struct {
	name      string
	attrs     uint32
	tag       uint32
	endOfFile int64
	allocated int64
	id        int64
	lastWrite int64
}

var dirBuffers = sync.Pool{New: func() any {
	buffer := make([]uint64, dirBufferWords)
	return &buffer
}}

// classify decide cómo tratar una entrada. Las carpetas reparse solo se recorren
// si son de nube: junctions, symlinks y etiquetas desconocidas pueden crear
// ciclos o contar dos veces lo mismo.
func classify(attrs, tag uint32) entryKind {
	isReparse := attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
	if attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		if isReparse && !isCloudTag(tag) {
			return kindLink
		}
		return kindDir
	}
	if isReparse && (tag == windows.IO_REPARSE_TAG_SYMLINK || tag == windows.IO_REPARSE_TAG_MOUNT_POINT) {
		return kindLink
	}
	return kindFile
}

func isCloudTag(tag uint32) bool {
	return tag&reparseTagCloudMask == reparseTagCloud || tag == reparseTagOneDrive
}

// measure devuelve lo que ocupa en disco y lo que falta por estar solo en nube.
func measure(attrs uint32, endOfFile, allocated int64) (size, cloud int64) {
	if attrs&cloudOnlyAttrs != 0 && endOfFile > allocated {
		return allocated, endOfFile - allocated
	}
	return allocated, 0
}

func readDir(path string, visit func(rawEntry)) error {
	pathPtr, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return err
	}

	// Abre solo la carpeta. Los archivos nunca se abren, así que no se descargan
	// los que están solo en nube.
	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_LIST_DIRECTORY|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)

	bufferPtr := dirBuffers.Get().(*[]uint64)
	defer dirBuffers.Put(bufferPtr)
	buffer := *bufferPtr
	start := unsafe.Pointer(&buffer[0])

	for {
		err := windows.GetFileInformationByHandleEx(
			handle,
			windows.FileIdBothDirectoryInfo,
			(*byte)(start),
			uint32(len(buffer)*8),
		)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return nil
		}
		if err != nil {
			return err
		}

		for offset := uintptr(0); ; {
			info := (*fileIDBothDirInfo)(unsafe.Add(start, offset))
			name := unsafe.Slice(&info.fileName[0], info.fileNameLength/2)
			if !isDotEntry(name) {
				entry := rawEntry{
					name:      windows.UTF16ToString(name),
					attrs:     info.fileAttributes,
					endOfFile: info.endOfFile,
					allocated: info.allocationSize,
					id:        info.fileID,
					lastWrite: fileTimeToUnix(info.lastWriteTime),
				}
				if info.fileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
					entry.tag = info.eaSize
				}
				visit(entry)
			}
			if info.nextEntryOffset == 0 {
				break
			}
			offset += uintptr(info.nextEntryOffset)
		}
	}
}

// fileTimeToUnix convierte un FILETIME (intervalos de 100 ns desde 1601) a
// segundos Unix.
func fileTimeToUnix(fileTime int64) int64 {
	const (
		intervalsPerSecond = 10_000_000
		secondsTo1970      = 11_644_473_600
	)
	return max(fileTime/intervalsPerSecond-secondsTo1970, 0)
}

func isDotEntry(name []uint16) bool {
	return (len(name) == 1 && name[0] == '.') ||
		(len(name) == 2 && name[0] == '.' && name[1] == '.')
}

// longPath evita el límite de 260 caracteres de Windows.
func longPath(path string) string {
	if strings.HasPrefix(path, `\\`) {
		return path
	}
	return `\\?\` + path
}

// isGone indica que la carpeta desapareció durante el escaneo, lo cual es normal
// y no significa que falten permisos.
func isGone(err error) bool {
	return errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}

func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
