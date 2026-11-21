# El Buscador: estado y pendientes

Última actualización: 2026-11-03. Este documento permite retomar el trabajo en una sesión nueva. Reglas de producto y comandos: `CLAUDE.md`.

## Estado actual

**Fases 0 a 4 terminadas, más treemap, otros discos, duplicados por carpetas y Liberar espacio.** Hasta fase 3 está commiteado y publicado (`31d2756`). Lo demás está sin commitear.

La app escanea cualquier disco fijo, lista archivos y carpetas pesados, los muestra en un mapa, navega carpetas, marca Seguro/Revisar/No tocar, detecta basura dev, encuentra archivos duplicados y funciona en español e inglés. Limpia lo que el usuario confirma: manda a la papelera, ejecuta el comando oficial o pide liberar espacio en carpetas sincronizadas.

| Área | Archivos |
|---|---|
| Escáner | `internal/scanner/` |
| Reglas y semáforo | `internal/rules/rules.go` |
| Basura dev y consultas a git | `internal/devtrash/` |
| Limpieza (lo único que modifica disco) | `internal/actions/` |
| Duplicados (solo lee) | `internal/duplicates/` |
| Servicios para la UI | `scanservice.go`, `diskservice.go`, `cleanupservice.go`, `duplicateservice.go` |
| Interfaz | `frontend/src/main.tsx`, `frontend/src/cleanup.tsx`, `frontend/src/duplicates.tsx`, `frontend/src/map.tsx`, `frontend/src/i18n.ts` |
| Prueba de interfaz | `scripts/e2e.mjs`, `scripts/e2e-cleanup.mjs`, `scripts/e2e-duplicates.mjs`, `scripts/e2e-drives.mjs`, `scripts/e2e-update.mjs`, `e2e.go` (solo con `-tags e2e`) |
| Actualizaciones y publicación | `updateservice.go`, `frontend/src/update.tsx`, `scripts/release/`, `.github/workflows/release.yml` |
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
node scripts/e2e-cleanup.mjs bin bin/e2e.exe     # limpia un fixture temporal y lo restaura
node scripts/e2e-duplicates.mjs bin bin/e2e.exe  # igual, con duplicados
MSYS2_ARG_CONV_EXCL='*' node scripts/e2e-drives.mjs bin bin/e2e.exe  # selector de disco, con subst temporal
node scripts/e2e-update.mjs bin                  # actualiza de 0.0.1 a 0.0.2 con servidor local
```

Búsqueda real de duplicados, solo lectura: `EL_BUSCADOR_REAL_SCAN='C:\' go test ./internal/duplicates -run TestRealSearch -v -count=1 -timeout 580s`. Se corta a los 8 minutos, o con `EL_BUSCADOR_REAL_LIMIT`.

Revisión real de Liberar espacio, solo lectura: `EL_BUSCADOR_REAL_FREEUP='C:\ruta' go test ./internal/actions -run TestRealFreeUpSurvey -v -count=1`.

`frontend/bindings/` no está en el repo. `wails3 build` la regenera. Tras cambiar un servicio, para `tsc`: `wails3 generate bindings -clean=true -ts`.

- `wails3 build` imprime errores del módulo Terminal-Icons de PowerShell. No afectan: revisa el código de salida.
- `scripts/e2e.mjs` puede dejar vivo el proceso de prueba. Ciérralo con `taskkill //F //IM e2e.exe`.
- Análisis real, solo lectura: `EL_BUSCADOR_REAL_SCAN='C:\' go test ./internal/devtrash -run TestRealAnalyze -v -count=1`.

## Siguiente paso: probar Liberar espacio de verdad

Es lo único de esta tanda que no se ha ejecutado sobre un archivo real. Ver "Funciones agregadas para el primer tester". Después: fase 5.

## Funciones agregadas para el primer tester

- **Mapa** (`frontend/src/map.tsx`, `Result.Tree`): dos niveles, y una carpeta que es 90% o más de su padre no gasta nivel. Colores validados con el script de dataviz, siempre con símbolo además del color.
- **Otros discos:** se elige en la pantalla inicial. Solo discos fijos: fuera red y extraíbles.
  - Las carpetas de sistema se protegen por nombre en la raíz de cualquier disco.
  - `IsSynced` también mira las variables de OneDrive y la raíz de cada disco.
- **Duplicados por carpetas:** opcional. "Conservar lo de X" ahora incluye subcarpetas; solo con la carpeta exacta no servía en árboles de fotos.
- **Liberar espacio** (`internal/actions/freeup_windows.go`): quita el fijado y marca como no fijado, igual que el Explorador. No borra ni abre archivos. Solo actúa sobre archivos con marca de nube que Windows reporta como ya subidos.

Lo que no es obvio en el código:

- **Windows esconde a los programas la marca de nube** (`PHCM_DISGUISE_PLACEHOLDERS`). `FindFirstFile` los muestra como archivos normales. `exposePlaceholders` lo cambia solo en ese hilo. Sin esto, todo cuenta como local y la función no hace nada. Los tests con disco simulado no lo detectaron; lo detectó la revisión real.
- **`placeholderInSync` responde false ante la duda.** En este equipo descartó 101 archivos de `iCloudDrive` que no han subido, unos 304 GB de 403 GB.

### Por verificar de esta tanda

- [ ] **Pulsar Liberar espacio en una carpeta chica real**, y comprobar: que el espacio baja, que el archivo sigue visible y que se vuelve a descargar al abrirlo. Solo se ha ejecutado la vista previa, que no cambia nada. La escritura se probó con disco simulado.
- [ ] Liberar espacio con OneDrive activo: aquí sus 3,911 archivos no tienen marca de nube, así que la app se niega. No se sabe si es porque OneDrive no está corriendo.
- [ ] Otros discos con un segundo disco real: este equipo solo tiene `C:`. Se probó con `subst`, que sirve para el selector y el escaneo, pero la limpieza ahí se rechaza a propósito. Mandar a papelera en otro disco no se ha ejecutado.
- [ ] Una unidad `subst` aparece como disco fijo. Escanea bien y no deja limpiar. Revisar si conviene ocultarla.
- [ ] El diálogo de Windows para elegir carpetas en Duplicados: el E2E lo reemplaza con respuestas preparadas.
- [ ] Revisar a ojo el mapa en inglés y con la ventana chica.

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
- La barra de selección suma de más si se marca una carpeta y algo dentro de ella. La confirmación ya muestra el total correcto.

## Fase 4: duplicados (terminada)

Compara por tamaño, luego hash parcial (primeros y últimos 64 KB) y al final BLAKE3 completo. Caché SQLite en `%LOCALAPPDATA%\el-buscador\hashes.db`, por ruta + tamaño + FILETIME.

Decisiones tomadas:

- **Se elige por grupo o con "Conservar lo de esta carpeta".** Nada viene preseleccionado, y un grupo sin copias en esa carpeta queda sin marcar.
- **No se comparan:** menos de 1 MiB, archivos solo en la nube, hard links, enlaces y carpetas de programas o generadas (`devtrash.SkipForDuplicates`). Va por nombre de carpeta: una carpeta propia llamada `build` o `target` tampoco se compara. La pantalla dice cuánto quedó sin comparar por estar en la nube.
- **Toda copia es Revisar.** Lo rastreado por git, No tocar o Manual se puede conservar, pero no se ofrece para borrar.
- **Lo que no cabe en la papelera no se manda.** Cabe = capacidad menos lo que ya tiene, porque al pasarse Windows descarta de forma permanente lo más viejo. La confirmación bloquea y avisa, y `Cleaner.reserve` lo hace cumplir en el backend, también en Basura dev. Duplicados ofrece "Dejar solo lo que cabe". Se mide al confirmar: si algo más llena la papelera después, puede quedarse corto.

Lo que no es obvio en el código:

- **`VerifyCopy` relee ambos archivos completos antes de mover cada copia y nunca usa la caché.** Es lo que impide dejar al usuario sin copia si algo cambió tras la búsqueda.
- **`planChoices` admite una sola copia garantizada por grupo.** Sin eso, "conservar A, borrar B" más "conservar B, borrar A" sumarían para borrar todo.
- **Los radios no llevan `name`:** con varias copias conservadas en la misma carpeta, un `name` compartido solo mostraría una marcada.

Medido en el equipo de Kabir (2026-11-03): búsqueda de 3 min 16 s sobre 19,242 candidatos, con 208 GB leídos. Encontró 3,757 grupos y 126.6 GB recuperables. Otros 1,711 archivos (13 GB) no se compararon por estar en la nube. No sirve para anunciar velocidad: es una sola medición en un solo disco.

### Por verificar en fase 4

- [ ] Límite real de la papelera: se probó con su espacio simulado (`EL_BUSCADOR_E2E_BIN_ROOM`), sin llenarla de verdad. Que Windows descarte lo más viejo al pasarse es lo que se espera de la papelera, pero no se observó aquí.
- [ ] Placeholder real de iCloud u OneDrive: los tests usan el atributo `OFFLINE`. Lo observado en la búsqueda real es indirecto (el disco libre bajó solo 64 MB). Falta confirmar que `FILE_FLAG_OPEN_NO_RECALL` y abrir un archivo de nube ya descargado funcionan bien.
- [ ] Revisar a ojo la lista con miles de grupos en la app real, incluidos "Mostrar más" y el progreso. Las capturas son de un fixture con 3 grupos.
- [ ] Cancelar y continuar desde la app: la caché se probó en tests, no a mano.
- [ ] La caché nunca borra filas viejas. Son pocos datos, pero crece con cada ruta nueva.

## Publicar y actualizar

Listo en código, sin ejecutar contra GitHub. Nada se ha publicado.

**Falta decidir:** el repo `stkabir/el-buscador` es privado, y las copias instaladas no pueden leer los releases de un repo privado sin incrustar un token, lo que no se debe hacer. Hay que hacerlo público, o publicar en un repo público aparte.

**Primera vez:**

```bash
gh secret set EL_BUSCADOR_SIGNING_KEY < ~/.el-buscador/update-signing-key.txt
```

**Cada versión:**

```bash
git tag v0.1.0 && git push origin v0.1.0
```

El workflow `.github/workflows/release.yml` compila, prueba, firma y crea el release con el `.exe`, su firma, los checksums y el instalador por usuario. Solo las etiquetas `vX.Y.Z` lo disparan.

Cómo funciona:

- Usa `pkg/updater` de Wails: reemplaza el `.exe`, conserva respaldo y reinicia. `signedProvider` (`updateservice.go`) le agrega la firma, porque el proveedor de GitHub de Wails solo trae checksum.
- **La llave privada** está en `~/.el-buscador/update-signing-key.txt`, nunca en el repo. La pública está en `updatePublicKeyHex`. **Respaldarla.** Sin ella, las copias instaladas no aceptan versiones nuevas y hay que reinstalar. Si se filtra, quien la tenga puede publicar actualizaciones que la app acepta.
- Al abrir revisa si hay versión nueva (una petición a GitHub) y avisa. Nada se instala sin clic, ni con un escaneo o limpieza en curso.
- `version` es `dev` salvo en releases: `wails3 build` normal no busca actualizaciones.

### Por verificar al publicar

- [ ] El workflow nunca ha corrido: instalar NSIS, los tests en el runner (usan la papelera real) y crear el release.
- [ ] Instalador por usuario: `makensis` no está en este equipo. Comprobar que instala en `%LOCALAPPDATA%\Programs`, donde el updater puede escribir. Instalar en `Program Files` haría fallar la actualización.
- [ ] Progreso de descarga: en el E2E termina demasiado rápido para verlo.
- [ ] Actualizar con la app abierta en otro equipo y con conexión lenta.
- [ ] SmartScreen: sin firma de código avisa al descargar el instalador. Las actualizaciones las baja la app, así que no deberían disparar ese aviso, pero no se ha comprobado.

## Fase 5: publicación

- Landing con Astro + Tailwind.
- Distribución por `winget` y `scoop`.
- README con límites conocidos.
- Revisar el nombre: "El Buscador" cuesta encontrarlo en Google. Decidir antes de comprar dominio.
- Firma de código: sin ella, SmartScreen alerta a usuarios. Azure Artifact Signing no admite México. Opciones por revisar: SignPath Foundation (gratis, requiere repo público y proyecto con visibilidad) y Certum Open Source (~€25-49).

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

- [ ] **Disco casi lleno:** ~21.6 GB libres. `C:\Users\kabir\iCloudDrive\series` sigue ocupando 411 GB. Confirmar en iCloud.com que subieron los 338 archivos y luego usar clic derecho, "Liberar espacio". **No borrar la carpeta: el borrado se sincroniza y elimina las series de iCloud.** La app ya distingue lo subido: el 2026-11-04 Windows reportaba 110 archivos (97-99 GB) listos y unos 100 archivos (~304 GB) aún sin subir en `iCloudDrive`.
- [ ] **Espacio rápido:** `npm cache clean --force` libera hasta 14.5 GB. No se ha corrido.
- [ ] **`staytrue/CLAUDE.md` desactualizado:** dice que `dist/` está commiteado, pero `.gitignore` lo excluye y git rastrea 0 archivos ahí. Falta decidir si se corrige.
- [ ] **Espacio sin identificar:** libres bajaron de 28.5 a 18.3 GB durante la sesión y luego subieron a ~21.6 GB. Go y el proyecto explican ~1.5 GB. No se encontró el resto.
- [ ] **Fotos:** `iCloudPhotos` y `Fototeca_Final` suman ~229 GB y podrían repetirse. La pestaña Duplicados ya los compara, salvo lo que esté solo en iCloud. **Borrar del lado de `iCloudPhotos` también borra de iCloud:** conservar esa copia y borrar del otro lado, o confirmar primero.
