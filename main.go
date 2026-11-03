package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

// Estas variables solo se llenan en builds de prueba (ver e2e.go).
var (
	extraBrowserArgs []string
	scanRootOverride string
)

func main() {
	scan := &ScanService{}
	app := application.New(application.Options{
		Name:        "El Buscador",
		Description: "Analizador de disco que explica qué puedes borrar",
		Services: []application.Service{
			application.NewService(&DiskService{}),
			application.NewService(scan),
			application.NewService(&CleanupService{scan: scan}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Windows: application.WindowsOptions{
			AdditionalBrowserArgs: extraBrowserArgs,
		},
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
