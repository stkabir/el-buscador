# El Buscador: estado y pendientes

Última actualización: 2026-11-03. Este documento permite retomar el trabajo en una sesión nueva. Reglas de producto y comandos: `CLAUDE.md`.

## Estado actual

**Fases 0, 1, 2 y 3 terminadas.** Fases 0-2 están en el commit `861fcea`. Fase 3 está sin commitear.

La app escanea el disco del sistema, lista archivos y carpetas pesados, navega carpetas, marca Seguro/Revisar/No tocar, detecta basura dev y funciona en español e inglés. Limpia lo que el usuario confirma: manda a la papelera o ejecuta el comando oficial.

| Área | Archivos |
|---|---|
| Escáner | `internal/scanner/` |
| Reglas y semáforo | `internal/rules/rules.go` |
| Basura dev y consultas a git | `internal/devtrash/` |
| Limpieza (lo único que modifica disco) | `internal/actions/` |
| Servicios para la UI | `scanservice.go`, `diskservice.go`, `cleanupservice.go` |
| Interfaz | `frontend/src/main.tsx`, `frontend/src/cleanup.tsx`, `frontend/src/i18n.ts` |
| Prueba de interfaz | `scripts/e2e.mjs`, `scripts/e2e-cleanup.mjs`, `e2e.go` (solo con `-tags e2e`) |
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
node scripts/e2e.mjs bin bin/e2e.exe     # solo lee; guarda capturas en bin/
node scripts/e2e-cleanup.mjs bin bin/e2e.exe  # limpia un fixture temporal y lo restaura
```

`frontend/bindings/` no está en el repo. `wails3 build` la regenera. Tras cambiar un servicio, para `tsc`: `wails3 generate bindings -clean=true -ts`.

- `wails3 build` imprime errores del módulo Terminal-Icons de PowerShell. No afectan: revisa el código de salida.
- `scripts/e2e.mjs` puede dejar vivo el proceso de prueba. Ciérralo con `taskkill //F //IM e2e.exe`.
- Análisis real, solo lectura: `EL_BUSCADOR_REAL_SCAN='C:\' go test ./internal/devtrash -run TestRealAnalyze -v -count=1`.

## Siguiente paso: commit de fase 3 y prueba con datos reales

Fase 3 no está commiteada. Antes de empezar fase 4, ver "Por verificar en fase 3".

## Fase 3: limpieza (terminada)

Decisiones tomadas:

- **`.next` y `.nuxt`:** Seguro si git los ignora. **`dist` y `build`:** además exigen script `build` en `package.json`. Si no, Revisar. `package.json` no se lee si está solo en la nube.
- **Carpetas enormes:** fallan y avisan. La app no ofrece borrado permanente, ni como opción.
- **Otros discos:** bloqueados en el backend. Solo se limpia el disco del sistema.
- **Elementos que se quitan desde su app** (`Advice.Manual`: `.vhdx`, `.vmdk`, Docker, `Windows.old`, `hiberfil.sys`): no se ofrecen aunque sean Revisar.
- **Caché sin su herramienta instalada:** va a la papelera, y la confirmación lo dice antes. Si la herramienta desaparece después de confirmar, falla sin caer en la papelera.

Lo que no es obvio en el código:

- **La papelera no libera disco hasta vaciarla.** Por eso el resultado separa "en la papelera" de lo que el disco ganó según Windows. La app abre la papelera, pero nunca la vacía.
- **Nunca usar `FOF_WANTNUKEWARNING`:** con esa opción Windows espera un clic en una ventana y no llega al sink. Sin ella, cuando la papelera rechaza algo, Windows reintenta sin papelera, pasa por `sinkPreDelete` y el veto lo detiene. Comprobado con una carpeta demasiado profunda.
- **COM escribe en `comOut`, global, no en variables locales.** Con locales, Go puede mover la pila y se pierde lo que escribe Windows. Falló de forma intermitente al desarrollarlo.

### Por verificar en fase 3

- [ ] Ejecutar comandos reales desde la app: `npm cache clean --force` es el candidato. Los tests usan `cmd` y `ping`; ningún comando de limpieza real se ha ejecutado.
- [ ] Rechazo de la papelera por tamaño o por estar desactivada. Solo se probó el rechazo por ruta profunda, y la lectura de capacidad contra un tamaño imposible. Probarlo de verdad requiere cambiar la configuración de la papelera, y reducir su capacidad puede vaciarla.
- [ ] Mandar a papelera un `node_modules` grande, y medir cuánto tarda.
- [ ] Revisar a ojo la vista con comando, la de carpetas sincronizadas y el progreso. Las capturas solo cubren lista, confirmación y resultado.
- [ ] Probar "Abrir en el Explorador" y "Abrir la papelera" a mano: abren ventanas, así que el E2E no los cubre.
- [ ] Probar symlink real: el test se salta sin permiso. El junction sí se probó.

### Límites conocidos

- Entre validar y mover hay un instante en que la ruta podría cambiar. Se valida justo antes, elemento por elemento, sin bloquear el archivo.
- Al llenarse, la papelera descarta lo más viejo que ya tenía, como en cualquier borrado de Windows. La app no avisa.
- La barra de selección suma de más si se marca una carpeta y algo dentro de ella. La confirmación ya muestra el total correcto.

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
