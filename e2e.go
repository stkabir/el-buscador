//go:build e2e

package main

import "os"

// Abre el puerto de depuración para pruebas de interfaz y permite escanear solo una
// carpeta temporal, para probar la limpieza sin tocar nada real. Este archivo nunca
// entra en builds normales: requiere `go build -tags e2e`.
func init() {
	extraBrowserArgs = []string{"--remote-debugging-port=9333"}
	scanRootOverride = os.Getenv("EL_BUSCADOR_E2E_ROOT")
}
