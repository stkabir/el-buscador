# El Buscador

Analizador de disco para Windows que muestra qué ocupa espacio y explica qué puedes borrar con seguridad.

En desarrollo. Hoy escanea el disco del sistema, muestra lo que más pesa, explica qué se puede borrar y detecta basura de desarrollo, en español e inglés. Solo lee: todavía no borra nada.

Límite conocido: los archivos compartidos por hard links, como los de pnpm, se cuentan una sola vez, en la primera carpeta donde aparecen. Por eso el tamaño de cada `node_modules` puede variar entre escaneos, aunque el total no se duplica.

## Desarrollo

Requiere Go, pnpm y la CLI de Wails v3:

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
wails3 dev      # app con recarga en vivo
wails3 build    # genera bin/el-buscador.exe
go test ./...
```

Prueba de interfaz de punta a punta, con el puerto de depuración que solo existe en builds `e2e`:

```bash
go build -tags e2e -o bin/e2e.exe .
node scripts/e2e.mjs bin bin/e2e.exe   # guarda capturas en bin/
```

## Licencia

[GPL-3.0](LICENSE)
