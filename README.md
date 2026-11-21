# El Buscador

Analizador de disco para Windows que muestra qué ocupa espacio y explica qué puedes borrar con seguridad.

En desarrollo. Hoy escanea cualquier disco fijo, muestra lo que más pesa, también en un mapa, explica qué se puede borrar, detecta basura de desarrollo y encuentra archivos duplicados, en español e inglés. Limpia lo que confirmas: manda a la papelera o, en cachés, ejecuta el comando oficial de la herramienta, que te muestra antes. En carpetas sincronizadas puede liberar espacio sin borrar nada de la nube.

Límite conocido: los archivos compartidos por hard links, como los de pnpm, se cuentan una sola vez, en la primera carpeta donde aparecen. Por eso el tamaño de cada `node_modules` puede variar entre escaneos, aunque el total no se duplica.

## Actualizaciones

Al abrir, la app pregunta a GitHub si hay una versión nueva y avisa. Es la única conexión que hace. Nada se instala sin tu clic, y cada actualización se verifica con una firma antes de instalarse.

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
