# El Buscador

Analizador de disco de escritorio, open source. Audiencia inicial: devs, con UI simple que también sirva a usuarios inexpertos. Windows primero.

Estado, siguiente paso y pendientes: `PENDIENTE.md`. Léelo al retomar.

## Stack

Go + Wails v3, UI con Vite + Preact + Tailwind v4 en `frontend/`. pnpm únicamente.

## Comandos

```bash
wails3 dev     # desarrollo
wails3 build   # bin/el-buscador.exe
go test ./...
```

`frontend/bindings/` se genera desde los servicios Go.

## Reglas de producto

- Pantalla inicial con una sola acción principal. Lo técnico queda como opcional.
- Texto de UI en español e inglés, en lenguaje simple.
- Escanear nunca abre archivos solo en nube, para no disparar descargas.
- Toda recomendación de borrado explica el motivo. Lo rastreado por git nunca es "seguro".
- Borrar solo manda a papelera y siempre pide confirmación. Nada se borra automáticamente.
- Nunca probar borrado fuera de carpetas temporales de test.
