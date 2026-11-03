# El Buscador: estado y pendientes

Última actualización: 2026-11-02. Este documento permite retomar el trabajo en una sesión nueva. Reglas de producto y comandos: `CLAUDE.md`.

## Estado actual

**Fases 0, 1 y 2 terminadas. Nada está commiteado:** el repo existe en `main`, sin ningún commit.

La app escanea el disco del sistema, lista archivos y carpetas pesados, navega carpetas, marca Seguro/Revisar/No tocar, detecta basura dev y funciona en español e inglés. Solo lee: no borra nada.

| Área | Archivos |
|---|---|
| Escáner | `internal/scanner/` |
| Reglas y semáforo | `internal/rules/rules.go` |
| Basura dev y consultas a git | `internal/devtrash/` |
| Servicios para la UI | `scanservice.go`, `diskservice.go` |
| Interfaz | `frontend/src/main.tsx`, `frontend/src/i18n.ts` |
| Prueba de interfaz | `scripts/e2e.mjs`, `e2e.go` (solo con `-tags e2e`) |
| Logos | `design/logo/` |

Decisiones ya tomadas:

- **Stack:** Go + Wails v3, Vite + Preact + Tailwind, pnpm.
- **Alcance:** Windows primero, devs primero con UI simple.
- **Licencia:** GPL-3.0, todo open source.
- **Logo:** "Mapa del disco" (`design/logo/6-mapa-del-disco.svg`, ícono en `design/logo/app-icon.svg`).

## Cómo retomar

Go y `wails3` pueden no estar en el PATH de una terminal ya abierta. En Git Bash:

```bash
export PATH="$PATH:/c/Program Files/Go/bin:$HOME/go/bin"
cd /c/laragon/www/el-buscador

go test ./internal/... -count=1
wails3 build                             # genera bin/el-buscador.exe
pnpm --dir frontend exec tsc --noEmit

go build -tags e2e -o bin/e2e.exe .
node scripts/e2e.mjs bin bin/e2e.exe     # guarda capturas en bin/
```

- `wails3 build` imprime errores del módulo Terminal-Icons de PowerShell. No afectan: revisa el código de salida.
- `scripts/e2e.mjs` puede dejar vivo el proceso de prueba. Ciérralo con `taskkill //F //IM e2e.exe`.
- Análisis real, solo lectura: `EL_BUSCADOR_REAL_SCAN='C:\' go test ./internal/devtrash -run TestRealAnalyze -v -count=1`.

## Siguiente paso: primer commit

Hacerlo antes de fase 3, para tener punto de regreso. Falta la confirmación de Kabir.

Antes de commitear, decidir si `frontend/bindings/` entra al repo: se genera desde Go y hoy no está en `.gitignore`. Recomendación: ignorarla, siempre que un clon limpio compile con `wails3 build`. Hay que probarlo.

## Fase 3: limpieza

Primera función que modifica disco. Un bug aquí destruye datos de usuarios, así que cada regla necesita test.

### Reglas

- Borrar solo manda a la papelera. Nunca borrado permanente.
- Confirmación siempre, con lista de rutas y total de GB. Nada automático.
- Antes de borrar, volver a comprobar la ruta en el momento: que exista, que no sea enlace y que su semáforo no sea No tocar. El escaneo puede estar viejo.
- Bloquear rutas de sistema en el backend, no solo en la UI: `Windows`, `Program Files`, `ProgramData`, raíces de disco y la carpeta de usuario.
- Elementos Seguro: se pueden seleccionar.
- Elementos Revisar: requieren una confirmación adicional.
- Elementos No tocar o sin clasificar: la limpieza guiada no los ofrece.
- Carpetas sincronizadas: mostrar que el borrado se propaga a la nube.
- Cachés: preferir comando oficial y mostrarlo antes de ejecutar. Ya están en `findCaches` (`internal/devtrash/devtrash.go`).
- Tras limpiar, mostrar espacio realmente liberado según el disco, no el estimado.

### Tareas

1. Crear `internal/actions/` con envío a papelera mediante `IFileOperation` o `SHFileOperation`.
2. Agregar validación de rutas previa al borrado.
3. Agregar ejecución de comandos oficiales, sin shell, con salida visible y manejo de "herramienta no instalada".
4. Exponerlo en un servicio Wails nuevo.
5. Crear UI de selección, confirmación, progreso y resultado, en español e inglés.
6. Agregar enlace "Abrir en el Explorador" (solo lectura, útil para Revisar).

### Decisiones abiertas

- **`.next`, `dist` y similares con Seguro:** ¿basta con que git los ignore?
- **Carpetas enormes:** la papelera de Windows puede negarse a guardarlas o tardar mucho. Definir qué pasa: fallar y avisar, nunca caer en borrado permanente sin preguntar.
- **Otros discos:** hoy solo se escanea el disco del sistema.

### Verificación

- Probar borrado solo en carpetas temporales de test.
- Comprobar restauración desde la papelera.
- Probar: ruta bloqueada, ruta que cambió tras el escaneo, enlace, archivo en uso, cancelación a medias y herramienta no instalada.

## Fase 4: duplicados

- Comparar por tamaño, luego hash parcial y al final hash completo con BLAKE3.
- Caché en SQLite (`modernc.org/sqlite`, sin cgo), por ruta + tamaño + fecha.
- Excluir hard links y archivos solo en nube, para no disparar descargas.
- El usuario elige cuál conservar. Imposible borrar la última copia.
- Deshacer mediante papelera.
- Fixture obligatorio: mismo tamaño con distinto contenido no coincide.

## Fase 5: publicación

- Treemap con d3-hierarchy.
- Landing con Astro + Tailwind.
- GitHub Actions y releases.
- Distribución por `winget` y `scoop`.
- README con límites conocidos.
- Revisar el nombre: "El Buscador" cuesta encontrarlo en Google. Decidir antes de comprar dominio.
- Firma de código: sin ella, SmartScreen alerta a usuarios.

## Pendientes de fases ya hechas

### Por verificar

- [ ] Revisar a ojo la lista de proyectos en Basura dev, chips en Explorar y textos largos. Solo se vieron cachés y archivos en capturas.
- [ ] Comparar totales contra WizTree.
- [ ] Correr como administrador y ver cuánto bajan los 57 GB sin explicar. Su causa no está confirmada.
- [ ] Abrir `.exe` en PC limpia, sin Go ni Node.
- [ ] Probar symlinks reales: el test se salta sin permiso, y solo se probó su clasificación.
- [ ] Ver el ícono en la barra de tareas de Windows.
- [ ] Confirmar que la ventana se ve centrada a escala 125%. Una captura salió corrida, probablemente por el método de captura.

### Por mejorar

- [ ] `rootfs.vhdx` en `AppData` muestra el motivo genérico de AppData, no el de disco virtual. La regla heredada gana sobre la de extensión.
- [ ] Traducir el comando genérico de `dist` y `build`: hoy `commandEN` convierte el texto a mano.
- [ ] El tamaño por `node_modules` con pnpm varía entre escaneos por hard links. La app avisa, pero conviene calcular cuánto se liberaría de verdad.
- [ ] El tiempo de escaneo varió de 8 a 47 s con el disco en uso. No anunciar velocidad sin medirla mejor.
- [ ] Logo: se parece a íconos genéricos de layout. A 16 px sigue legible. Considerar wordmark en landing.
- [ ] Las imágenes de presentación del logo no se generaron. La skill requiere API key de Gemini.

### Roadmap posterior

Asistente para mover a disco externo o nube, "qué creció desde el último escaneo", fotos similares, lectura de MFT, Mac y Linux.

### Monetización

Código y compilación propia gratis. Ingresos previstos: instalador firmado con actualizaciones (~$15-25, pago único vía merchant of record; verificar condiciones desde México), GitHub Sponsors y soporte a empresas. Sin anuncios ni alertas alarmistas. Tratarlo como ingreso secundario.

## Fuera del proyecto

Asuntos del equipo de Kabir que siguen abiertos:

- [ ] **Disco casi lleno:** ~21.6 GB libres. `C:\Users\kabir\iCloudDrive\series` sigue ocupando 411 GB. Confirmar en iCloud.com que subieron los 338 archivos y luego usar clic derecho, "Liberar espacio". **No borrar la carpeta: el borrado se sincroniza y elimina las series de iCloud.**
- [ ] **Espacio rápido:** `npm cache clean --force` libera hasta 14.5 GB. No se ha corrido.
- [ ] **`staytrue/CLAUDE.md` desactualizado:** dice que `dist/` está commiteado, pero `.gitignore` lo excluye y git rastrea 0 archivos ahí. Falta decidir si se corrige.
- [ ] **Espacio sin identificar:** libres bajaron de 28.5 a 18.3 GB durante la sesión y luego subieron a ~21.6 GB. Go y el proyecto explican ~1.5 GB. No se encontró el resto.
- [ ] **Fotos:** `iCloudPhotos` y `Fototeca_Final` suman ~229 GB y podrían repetirse. Comparar por hash antes de borrar nada.
