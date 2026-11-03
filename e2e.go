//go:build e2e

package main

// Abre el puerto de depuración para pruebas de interfaz. Este archivo nunca entra
// en builds normales: requiere `go build -tags e2e`.
func init() {
	extraBrowserArgs = []string{"--remote-debugging-port=9333"}
}
