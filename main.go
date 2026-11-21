package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

//go:embed all:frontend/dist
var assets embed.FS

// Estas variables solo se llenan en builds de prueba (ver e2e.go).
var (
	extraBrowserArgs []string
	scanRootOverride string
	// binRoomOverride simula cuánto cabe en la papelera. Negativo: se mide de verdad.
	binRoomOverride int64 = -1
	// folderPicks responde al diálogo de carpetas, que una prueba no puede manejar.
	folderPicks []string
)

func main() {
	scan := &ScanService{}
	cleanup := &CleanupService{scan: scan}
	dups := &DuplicateService{scan: scan}
	updates := &UpdateService{scan: scan, cleanup: cleanup, dups: dups}
	app := application.New(application.Options{
		Name:        "El Buscador",
		Description: "Analizador de disco que explica qué puedes borrar",
		Services: []application.Service{
			application.NewService(&DiskService{}),
			application.NewService(scan),
			application.NewService(cleanup),
			application.NewService(dups),
			application.NewService(updates),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Windows: application.WindowsOptions{
			AdditionalBrowserArgs: extraBrowserArgs,
		},
	})

	// Un fallo aquí solo apaga las actualizaciones: la app sigue sirviendo.
	enabled, err := setupUpdater(app)
	if err != nil {
		app.Logger.Error("configurar actualizaciones", "error", err)
	}
	updates.enabled = enabled
	app.Event.On(updater.EventDownloadProgress, func(event *application.CustomEvent) {
		if progress, ok := event.Data.(updater.Progress); ok {
			updates.setProgress(progress.Written, progress.Total)
		}
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "El Buscador",
		Width:            1000,
		Height:           618,
		BackgroundColour: application.NewRGB(15, 17, 21),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// nextFolderPick devuelve la siguiente respuesta preparada para el diálogo de carpetas.
func nextFolderPick() string {
	if len(folderPicks) == 0 {
		return ""
	}
	pick := folderPicks[0]
	folderPicks = folderPicks[1:]
	return pick
}
