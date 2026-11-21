// Package actions es lo único de la app que modifica el disco. Solo manda a la
// papelera o ejecuta comandos oficiales de limpieza, y valida cada ruta justo antes.
package actions

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/windows"

	"el-buscador/internal/rules"
)

// Code dice por qué no se tocó un elemento. La interfaz lo traduce.
type Code string

const (
	CodeNotScanned   Code = "notScanned"
	CodeInvalidPath  Code = "invalidPath"
	CodeProtected    Code = "protected"
	CodeMissing      Code = "missing"
	CodeLink         Code = "link"
	CodeChanged      Code = "changed"
	CodeKeep         Code = "keep"
	CodeUnclassified Code = "unclassified"
	CodeManual       Code = "manual"
	CodeNeedsConfirm Code = "needsConfirm"
	CodeTooBig       Code = "tooBig"
	CodeBinOff       Code = "binOff"
	// CodeBinFull: cabría en una papelera vacía, pero hoy haría que Windows descarte
	// lo que ya tiene.
	CodeBinFull      Code = "binFull"
	CodeBinRejected  Code = "binRejected"
	CodeInUse        Code = "inUse"
	CodeNoPermission Code = "noPermission"
	CodeToolMissing  Code = "toolMissing"
	CodeToolFailed   Code = "toolFailed"
	CodeCanceled     Code = "canceled"
	CodeFailed       Code = "failed"
	// CodeNotDuplicate: la copia ya no es idéntica. CodeNoCopyLeft: la copia que se
	// conserva ya no está intacta, así que borrar dejaría sin ninguna.
	CodeNotDuplicate Code = "notDuplicate"
	CodeNoCopyLeft   Code = "noCopyLeft"
)

// Refused es una negativa a tocar una ruta.
type Refused struct{ Code Code }

func (r *Refused) Error() string { return "rechazado: " + string(r.Code) }

func refuse(code Code) error { return &Refused{Code: code} }

// Refuse crea una negativa desde otro paquete.
func Refuse(code Code) error { return refuse(code) }

// CodeOf devuelve el motivo de una negativa, o "" si err es otra cosa.
func CodeOf(err error) Code {
	var refused *Refused
	if errors.As(err, &refused) {
		return refused.Code
	}
	return ""
}

// Classifier devuelve el semáforo actual de una ruta. El segundo valor es false si
// no hay regla.
type Classifier func(path string, isDir bool) (rules.Advice, bool)

// Guard decide si una ruta se puede limpiar en este momento.
type Guard struct {
	Env      rules.Env
	Classify Classifier
	// Scanned dice si el escaneo vio la ruta y si era carpeta. Nil no lo comprueba.
	Scanned func(path string) (isDir, known bool)
}

// Checked es lo que Guard comprobó de una ruta.
type Checked struct {
	IsDir  bool
	Advice rules.Advice
}

// Check valida path contra el disco actual, nunca contra el escaneo, que puede estar
// viejo. Un elemento Revisar exige confirmedReview.
func (g Guard) Check(path string, confirmedReview bool) (Checked, error) {
	if !isPlainDrivePath(path) {
		return Checked{}, refuse(CodeInvalidPath)
	}
	if isProtected(g.Env, path) {
		return Checked{}, refuse(CodeProtected)
	}
	var scannedDir bool
	if g.Scanned != nil {
		var known bool
		if scannedDir, known = g.Scanned(path); !known {
			return Checked{}, refuse(CodeNotScanned)
		}
	}

	info, err := inspect(path)
	if err != nil {
		return Checked{}, err
	}
	if info.link {
		return Checked{}, refuse(CodeLink)
	}
	if g.Scanned != nil && info.isDir != scannedDir {
		return Checked{}, refuse(CodeChanged)
	}
	// Distinta ruta real significa un enlace en algún padre o un nombre corto
	// (PROGRA~1): lo validado arriba no sería lo que se va a borrar.
	final, err := finalPath(path)
	if err != nil {
		return Checked{}, err
	}
	if !strings.EqualFold(final, path) {
		return Checked{}, refuse(CodeLink)
	}
	if !isFixedDrive(path) {
		return Checked{}, refuse(CodeInvalidPath)
	}

	advice, ok := g.Classify(path, info.isDir)
	checked := Checked{IsDir: info.isDir, Advice: advice}
	switch {
	case !ok:
		return checked, refuse(CodeUnclassified)
	case advice.Level == rules.LevelKeep:
		return checked, refuse(CodeKeep)
	case advice.Manual:
		return checked, refuse(CodeManual)
	case advice.Level == rules.LevelReview && !confirmedReview:
		return checked, refuse(CodeNeedsConfirm)
	case advice.Level != rules.LevelSafe && advice.Level != rules.LevelReview:
		return checked, refuse(CodeUnclassified)
	}
	return checked, nil
}

// isPlainDrivePath acepta solo rutas absolutas limpias tipo C:\a\b. Rechaza rutas de
// red o de dispositivo y flujos alternos (archivo.txt:oculto).
func isPlainDrivePath(path string) bool {
	if len(path) < 3 || path[1] != ':' || path[2] != '\\' {
		return false
	}
	letter := path[0] | 0x20
	if letter < 'a' || letter > 'z' || filepath.Clean(path) != path {
		return false
	}
	return !strings.ContainsAny(path[2:], `:*?"<>|/`)
}

// rootFolders son nombres que nunca se tocan, ni lo que tienen dentro, en la raíz de
// cualquier disco: un programa instalado en D: se rompe igual que en C:.
var rootFolders = []string{
	"$recycle.bin", "system volume information", "recovery", "boot", "efi",
	"pagefile.sys", "swapfile.sys", "hiberfil.sys",
	"windows", "program files", "program files (x86)", "programdata",
}

// systemFolders son las carpetas de este Windows, estén donde estén.
func systemFolders(env rules.Env) []string {
	folders := []string{env.Windir, env.ProgramData}
	return append(folders, env.ProgramFiles...)
}

var homeFolders = []string{"appdata", "desktop", "documents", "downloads", "music", "pictures", "videos"}

// isProtected bloquea las rutas de sistema. Sin los datos del sistema no se puede
// saber qué es seguro, así que bloquea todo.
func isProtected(env rules.Env, path string) bool {
	if env.SystemDrive == "" || env.Windir == "" || env.Home == "" {
		return true
	}
	norm := strings.ToLower(path)
	root := filepath.VolumeName(norm) + `\`
	if norm == root {
		return true
	}

	relative := norm[len(root):]
	first, _, _ := strings.Cut(relative, `\`)
	if slices.Contains(rootFolders, first) {
		return true
	}
	// La carpeta sincronizada completa nunca: borrarla se propaga a toda la nube.
	if first == relative && (first == "users" || rules.IsSyncFolderName(first)) {
		return true
	}

	folders := systemFolders(env)
	for _, folder := range folders {
		if folder == "" {
			continue
		}
		base := strings.ToLower(filepath.Clean(folder))
		if norm == base || strings.HasPrefix(norm, base+`\`) {
			return true
		}
	}

	// Una carpeta que contiene algo vital tampoco se puede borrar.
	vitals := append(folders, env.Home, env.LocalAppData, env.AppData)
	for _, vital := range append(vitals, env.SyncRoots...) {
		if vital != "" && strings.HasPrefix(strings.ToLower(filepath.Clean(vital))+`\`, norm+`\`) {
			return true
		}
	}

	home := strings.ToLower(filepath.Clean(env.Home))
	switch filepath.Dir(norm) {
	case filepath.Dir(home), filepath.Join(home, "appdata"):
		return true // un perfil de usuario, o AppData\Local y similares
	case home:
		name := filepath.Base(norm)
		return slices.Contains(homeFolders, name) || rules.IsSyncFolderName(name)
	}
	return false
}

type pathInfo struct {
	isDir bool
	link  bool
}

const (
	reparseTagCloudMask = 0xFFFF0FFF
	reparseTagCloud     = 0x9000001A
	reparseTagOneDrive  = 0x80000021
)

// inspect solo consulta atributos, así que no descarga archivos que están en la nube.
func inspect(path string) (pathInfo, error) {
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return pathInfo{}, refuse(CodeInvalidPath)
	}
	var data windows.Win32finddata
	handle, err := windows.FindFirstFile(wide, &data)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return pathInfo{}, refuse(CodeMissing)
		}
		return pathInfo{}, fmt.Errorf("leer %q: %w", path, err)
	}
	windows.FindClose(handle)

	info := pathInfo{isDir: data.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0}
	if data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		// Solo se aceptan los marcadores de archivos en la nube. Lo demás se trata
		// como enlace: symlinks, junctions y etiquetas desconocidas.
		tag := data.Reserved0
		info.link = tag&reparseTagCloudMask != reparseTagCloud && tag != reparseTagOneDrive
	}
	return info, nil
}

// finalPath resuelve la ruta real sin seguir el último enlace ni abrir su contenido.
func finalPath(path string) (string, error) {
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", refuse(CodeInvalidPath)
	}
	handle, err := windows.CreateFile(wide, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", fmt.Errorf("abrir %q: %w", path, err)
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", fmt.Errorf("resolver %q: %w", path, err)
	}
	return strings.TrimPrefix(windows.UTF16ToString(buffer[:n]), `\\?\`), nil
}

// isFixedDrive descarta discos de red y extraíbles: no tienen papelera confiable.
func isFixedDrive(path string) bool {
	wide, err := windows.UTF16PtrFromString(filepath.VolumeName(path) + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(wide) == windows.DRIVE_FIXED
}
