// Package rules explica en lenguaje simple qué es un archivo o carpeta y si se
// puede borrar. Lo que no tiene regla queda sin clasificar: nunca es "seguro" por
// omisión.
package rules

import (
	"os"
	"path/filepath"
	"strings"
)

// Level es el color del semáforo.
type Level string

const (
	LevelSafe   Level = "safe"
	LevelReview Level = "review"
	LevelKeep   Level = "keep"
)

// Text es un texto de interfaz en cada idioma.
type Text struct {
	ES string `json:"es"`
	EN string `json:"en"`
}

// Join une dos textos con un espacio.
func (t Text) Join(other Text) Text {
	return Text{ES: t.ES + " " + other.ES, EN: t.EN + " " + other.EN}
}

// Advice es la recomendación para un elemento. Action puede estar vacío. Manual
// indica que no se quita borrándolo, sino como dice Action, así que la limpieza no
// lo ofrece.
type Advice struct {
	Level  Level `json:"level"`
	Reason Text  `json:"reason"`
	Action Text  `json:"action"`
	Manual bool  `json:"manual"`
}

// Env son las carpetas del sistema contra las que se comparan las rutas.
type Env struct {
	SystemDrive  string
	Windir       string
	ProgramData  string
	ProgramFiles []string
	Home         string
	LocalAppData string
	AppData      string
}

// EnvFromOS lee las carpetas del sistema actual.
func EnvFromOS() Env {
	return Env{
		SystemDrive:  os.Getenv("SystemDrive"),
		Windir:       os.Getenv("WINDIR"),
		ProgramData:  os.Getenv("ProgramData"),
		ProgramFiles: []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")},
		Home:         os.Getenv("USERPROFILE"),
		LocalAppData: os.Getenv("LOCALAPPDATA"),
		AppData:      os.Getenv("APPDATA"),
	}
}

type pathRule struct {
	advice Advice
	// inherit aplica la regla también a todo lo que hay dentro de la ruta.
	inherit bool
}

// Catalog resuelve recomendaciones para las rutas de un sistema.
type Catalog struct {
	env   Env
	paths map[string]pathRule
}

// New crea el catálogo. Las variables vacías de env simplemente no generan regla.
func New(env Env) *Catalog {
	c := &Catalog{env: env, paths: make(map[string]pathRule)}
	drive := env.SystemDrive

	c.add(drive, `pagefile.sys`, false, Advice{
		Level:  LevelKeep,
		Reason: Text{"Memoria virtual: Windows la usa cuando se llena la RAM.", "Virtual memory: Windows uses it when RAM fills up."},
		Action: Text{"No la borres. Si necesitas espacio, reduce su tamaño en la configuración de memoria virtual.", "Don't delete it. If you need space, reduce its size in the virtual memory settings."},
	})
	c.add(drive, `swapfile.sys`, false, Advice{
		Level:  LevelKeep,
		Reason: Text{"Memoria virtual para apps de la Tienda. Windows la gestiona.", "Virtual memory for Store apps. Windows manages it."},
	})
	c.add(drive, `hiberfil.sys`, false, Advice{
		Level:  LevelReview,
		Manual: true,
		Reason: Text{"Guarda tu sesión para hibernar y para el inicio rápido.", "Stores your session for hibernation and fast startup."},
		Action: Text{"No la borres a mano. Se quita con powercfg /h off como administrador, y pierdes hibernación e inicio rápido.", "Don't delete it by hand. Remove it with powercfg /h off as administrator, and you lose hibernation and fast startup."},
	})
	c.add(drive, `System Volume Information`, true, Advice{
		Level:  LevelKeep,
		Reason: Text{"Puntos de restauración e índices de Windows.", "Windows restore points and indexes."},
	})
	c.add(drive, `Recovery`, true, Advice{
		Level:  LevelKeep,
		Reason: Text{"Herramientas para reparar Windows si deja de arrancar.", "Tools to repair Windows if it stops booting."},
	})
	c.add(drive, `$Recycle.Bin`, true, Advice{
		Level:  LevelReview,
		Manual: true,
		Reason: Text{"Tu papelera: son archivos que ya borraste.", "Your Recycle Bin: files you already deleted."},
		Action: Text{"Revisa que no necesites nada y vacíala desde el escritorio.", "Check you don't need anything, then empty it from the desktop."},
	})
	c.add(drive, `Windows.old`, true, Advice{
		Level:  LevelReview,
		Manual: true,
		Reason: Text{"Tu versión anterior de Windows. Sirve para regresar a ella.", "Your previous Windows version. It lets you roll back."},
		Action: Text{"Quítala con Configuración > Almacenamiento > Archivos temporales.", "Remove it from Settings > Storage > Temporary files."},
	})

	windows := Advice{
		Level:  LevelKeep,
		Reason: Text{"Archivos de Windows. Borrarlos puede impedir que arranque.", "Windows files. Deleting them can stop it from booting."},
	}
	c.add(env.Windir, ``, true, windows)
	temp := Advice{
		Level:  LevelSafe,
		Reason: Text{"Archivos temporales que las apps vuelven a crear.", "Temporary files that apps create again."},
		Action: Text{"Lo que esté en uso no se podrá borrar, y eso es normal.", "Anything in use can't be deleted, and that's normal."},
	}
	c.add(env.Windir, `Temp`, true, temp)
	c.add(env.LocalAppData, `Temp`, true, temp)
	c.add(env.Windir, `SoftwareDistribution\Download`, true, Advice{
		Level:  LevelReview,
		Manual: true,
		Reason: Text{"Descargas de Windows Update.", "Windows Update downloads."},
		Action: Text{"Límpialas con Configuración > Almacenamiento > Archivos temporales.", "Clean them from Settings > Storage > Temporary files."},
	})

	programs := Advice{
		Level:  LevelKeep,
		Reason: Text{"Programas instalados. Borrar aquí los rompe.", "Installed programs. Deleting here breaks them."},
		Action: Text{"Desinstálalos desde Configuración > Aplicaciones.", "Uninstall them from Settings > Apps."},
	}
	for _, dir := range env.ProgramFiles {
		c.add(dir, ``, true, programs)
	}
	c.add(env.ProgramData, ``, true, Advice{
		Level:  LevelReview,
		Reason: Text{"Datos compartidos de tus programas. Borrarlos puede romperlos.", "Shared program data. Deleting it can break them."},
	})
	c.add(env.Home, `AppData`, true, Advice{
		Level:  LevelReview,
		Reason: Text{"Datos y ajustes de tus apps. Borrarlos puede hacerte perder sesiones o configuración.", "App data and settings. Deleting them can lose sessions or configuration."},
	})
	c.add(env.LocalAppData, `Docker`, true, Advice{
		Level:  LevelReview,
		Manual: true,
		Reason: Text{"Disco de Docker: imágenes, contenedores y volúmenes. Los volúmenes pueden tener datos que no se recuperan.", "Docker's disk: images, containers and volumes. Volumes may hold data you can't recover."},
		Action: Text{"Limpia con docker system prune, que no toca volúmenes. El disco virtual puede no encogerse solo.", "Clean with docker system prune, which leaves volumes alone. The virtual disk may not shrink on its own."},
	})
	return c
}

func (c *Catalog) add(base, rel string, inherit bool, advice Advice) {
	if base == "" {
		return
	}
	// filepath.Join convierte "C:" + "x" en la ruta relativa "C:x".
	if strings.HasSuffix(base, ":") {
		base += `\`
	}
	c.paths[normalize(filepath.Join(base, rel))] = pathRule{advice: advice, inherit: inherit}
}

// Env devuelve las carpetas con las que se creó el catálogo.
func (c *Catalog) Env() Env { return c.env }

// IsSynced indica si la ruta está dentro de una carpeta sincronizada con la nube.
func (c *Catalog) IsSynced(path string) bool {
	if c.env.Home == "" {
		return false
	}
	home := normalize(c.env.Home) + `\`
	norm := normalize(path)
	if !strings.HasPrefix(norm, home) {
		return false
	}
	first, _, _ := strings.Cut(norm[len(home):], `\`)
	switch first {
	case "iclouddrive", "icloudphotos", "dropbox", "google drive":
		return true
	}
	return strings.HasPrefix(first, "onedrive")
}

// SyncedWarning explica qué pasa al borrar en una carpeta sincronizada.
func SyncedWarning() Text {
	return Text{
		"Esta carpeta se sincroniza: borrar aquí también borra de la nube y de tus otros dispositivos.",
		"This folder syncs: deleting here also deletes from the cloud and your other devices.",
	}
}

var extensions = map[string]Advice{
	".iso": {
		Level:  LevelReview,
		Reason: Text{"Imagen de disco. Suele poder descargarse otra vez.", "Disk image. It can usually be downloaded again."},
		Action: Text{"Confirma que tienes de dónde volver a bajarla.", "Confirm you can download it again."},
	},
	".vhdx": {
		Level:  LevelReview,
		Manual: true,
		Reason: Text{"Disco virtual. Borrarlo destruye lo que hay dentro de esa máquina.", "Virtual disk. Deleting it destroys what's inside that machine."},
		Action: Text{"Elimínalo desde la app que lo creó.", "Remove it from the app that created it."},
	},
	".vmdk": {
		Level:  LevelReview,
		Manual: true,
		Reason: Text{"Disco virtual. Borrarlo destruye lo que hay dentro de esa máquina.", "Virtual disk. Deleting it destroys what's inside that machine."},
		Action: Text{"Elimínalo desde la app que lo creó.", "Remove it from the app that created it."},
	},
	".dmp": {
		Level:  LevelSafe,
		Reason: Text{"Volcado de un error de una app. Solo sirve para diagnosticar.", "Crash dump from an app. It's only useful for diagnostics."},
	},
}

// Explain devuelve la recomendación para una ruta. El segundo valor es false si
// no hay regla.
func (c *Catalog) Explain(path string, isDir bool) (Advice, bool) {
	norm := normalize(path)
	for current := norm; ; {
		if rule, ok := c.paths[current]; ok && (rule.inherit || current == norm) {
			// Un disco virtual en AppData sigue siendo un disco virtual: su regla es
			// más específica que la de la carpeta, salvo que esta prohíba tocarlo.
			if ext, ok := extensions[strings.ToLower(filepath.Ext(path))]; ok && !isDir && ext.Manual && rule.advice.Level != LevelKeep {
				return ext, true
			}
			return rule.advice, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	if c.IsSynced(path) {
		return Advice{
			Level:  LevelReview,
			Reason: SyncedWarning(),
			Action: Text{"Para ganar espacio sin perderlo, haz clic derecho y elige Liberar espacio.", "To gain space without losing it, right-click and choose Free up space."},
		}, true
	}

	if !isDir {
		advice, ok := extensions[strings.ToLower(filepath.Ext(path))]
		return advice, ok
	}
	return Advice{}, false
}

func normalize(path string) string {
	return strings.ToLower(filepath.Clean(path))
}
