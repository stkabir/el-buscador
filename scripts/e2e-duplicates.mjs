// Prueba duplicados en la app real, escaneando solo una carpeta temporal.
// Uso: node scripts/e2e-duplicates.mjs <carpeta de capturas> <e2e.exe>
import { spawn, execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const [dir, exe] = process.argv.slice(2);
const port = 9333;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const MB = 1024 * 1024;
const bytes = (fill, size = MB) => Buffer.alloc(size, fill);

const root = mkdtempSync(join(tmpdir(), "el-buscador-e2e-dup-"));
const put = (rel, content) => {
  const path = join(root, rel);
  mkdirSync(join(path, ".."), { recursive: true });
  writeFileSync(path, content);
  return path;
};

// Grupo 1: cuatro copias en dos carpetas, dos de ellas en la que se conserva.
const keepA = put("fototeca/playa.jpg", bytes(1, 3 * MB));
const keepA2 = put("fototeca/playa copia.jpg", bytes(1, 3 * MB));
const dropA1 = put("respaldo/playa.jpg", bytes(1, 3 * MB));
const dropA2 = put("respaldo/viejo/playa.jpg", bytes(1, 3 * MB));
// Grupo 2: otra pareja en las mismas carpetas.
const keepB = put("fototeca/sol.jpg", bytes(2, 2 * MB));
const dropB = put("respaldo/sol.jpg", bytes(2, 2 * MB));
// Grupo 3: sin copia en la carpeta que se conserva, así que nada debe marcarse.
const untouchedA = put("otros/a/nota.bin", bytes(3));
const untouchedB = put("otros/b/nota.bin", bytes(3));
// Mismo tamaño y distinto contenido: nunca debe aparecer como grupo.
const different = Buffer.alloc(MB, 3);
different[MB / 2] = 9;
const lookalike = put("otros/c/parecida.bin", different);

const app = spawn(exe, [], { stdio: "ignore", env: { ...process.env, EL_BUSCADOR_E2E_ROOT: root, EL_BUSCADOR_E2E_BIN_ROOM: String(7 * MB), EL_BUSCADOR_E2E_PICKS: [join(root, "fototeca"), join(root, "respaldo")].join(";") } });

const restoreFromBin = () => {
  const script = `
$bin = (New-Object -ComObject Shell.Application).Namespace(10)
$bin.Items() | Where-Object { $bin.GetDetailsOf($_, 1) -like '${root.replace(/'/g, "''")}*' } | ForEach-Object { $_.InvokeVerb('undelete') }`;
  execFileSync("powershell", ["-NoProfile", "-NonInteractive", "-Command", script], { stdio: "inherit" });
};

let code = 0;
try {
  let page;
  for (let i = 0; i < 40 && !page; i++) {
    await sleep(500);
    try {
      const targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
      page = targets.find((t) => t.type === "page");
    } catch {}
  }
  if (!page) throw new Error("WebView2 no expuso página");

  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
  let id = 0;
  const pending = new Map();
  const errors = [];
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); }
    if (m.method === "Runtime.exceptionThrown") errors.push(m.params.exceptionDetails.text);
    if (m.method === "Runtime.consoleAPICalled" && m.params.type === "error") errors.push(JSON.stringify(m.params.args.map((a) => a.value ?? a.description)));
  };
  const send = (method, params = {}) => new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method, params })); });
  const evaluate = async (expression) => (await send("Runtime.evaluate", { expression, returnByValue: true })).result?.result?.value;
  const text = () => evaluate("document.body.innerText");
  const click = async (label) => {
    const ok = await evaluate(`(() => { const b = [...document.querySelectorAll('button')].filter(x => x.offsetParent !== null && x.innerText.trim().startsWith(${JSON.stringify(label)})).pop(); if (!b) return false; b.click(); return true; })()`);
    if (!ok) throw new Error(`Sin botón: ${label}. Pantalla: ${(await text()).slice(0, 500)}`);
  };
  const waitFor = async (needle, ms = 20000) => {
    const start = Date.now();
    while (Date.now() - start < ms) {
      if ((await text()).includes(needle)) return;
      await sleep(100);
    }
    throw new Error(`Timeout: ${needle}. Pantalla: ${(await text()).slice(0, 600)}`);
  };
  const shot = async (name) => {
    const { result } = await send("Page.captureScreenshot", { format: "png" });
    writeFileSync(`${dir}/${name}.png`, Buffer.from(result.data, "base64"));
  };
  const assert = (condition, message) => {
    if (!condition) throw new Error(`Falló: ${message}`);
    console.log(`  ok: ${message}`);
  };
  const button = (label) => evaluate(`(() => { const b = [...document.querySelectorAll('button')].filter(x => x.innerText.trim() === ${JSON.stringify(label)}).pop(); return b ? { disabled: b.disabled } : null; })()`);
  // Pulsa "Conservar lo de esta carpeta" en la fila de la ruta indicada.
  const keepFolderOf = async (path) => {
    const ok = await evaluate(`(() => { const row = [...document.querySelectorAll('fieldset > div')].find(d => d.innerText.includes(${JSON.stringify(path)})); if (!row) return false; [...row.querySelectorAll('button')].find(b => b.innerText.startsWith('Conservar lo de')).click(); return true; })()`);
    if (!ok) throw new Error(`Sin fila: ${path}`);
  };

  await send("Runtime.enable");
  await send("Page.enable");
  await waitFor("libres de");
  await click("Ver lo que más pesa");
  await waitFor("Encontramos", 60000);
  assert(!(await text()).includes("Buscar archivos repetidos"), "la pestaña oculta no se ve en las demás");
  await click("Duplicados");
  await waitFor("Buscar archivos repetidos");
  await sleep(200);
  await shot("d1-intro");

  // Solo entre dos carpetas: el grupo de "otros" no aparece.
  await click("Agregar carpeta");
  await waitFor("fototeca");
  await click("Agregar carpeta");
  await waitFor("respaldo");
  await shot("d1b-carpetas");
  await click("Comparar estas carpetas");
  await waitFor("Puedes recuperar hasta");
  const scoped = await text();
  assert(scoped.includes("en 2 grupos") && !scoped.includes("nota.bin"), "con carpetas elegidas, solo compara entre ellas");

  // Volver y quitarlas devuelve la búsqueda completa.
  await click("Cambiar carpetas");
  await waitFor("Comparar estas carpetas");
  for (let i = 0; i < 2; i++) {
    await evaluate(`document.querySelector('button[aria-label^="Quitar carpeta"]').click()`);
    await sleep(150);
  }
  await waitFor("Buscar duplicados");
  await click("Buscar duplicados");
  await waitFor("Puedes recuperar hasta");
  await sleep(300);
  await shot("d2-grupos");

  const results = await text();
  assert(results.includes("en 3 grupos"), "encuentra los 3 grupos");
  assert(!results.includes("parecida.bin"), "mismo tamaño con distinto contenido no aparece");

  // Conservar por carpeta marca dos grupos y deja el tercero sin tocar.
  await keepFolderOf(keepA);
  await waitFor("3 seleccionados");
  await shot("d3-marcados");
  const checked = await evaluate(`[...document.querySelectorAll('fieldset input[type=radio]')].filter(r => r.checked).length`);
  assert(checked === 3, "se ven marcadas las 3 copias que se conservan, incluidas las dos de la misma carpeta");
  await click("Limpiar…");
  await waitFor("¿Limpiar");
  await sleep(200);
  await shot("d4-confirmar");
  const confirm = await text();
  assert(confirm.includes(dropA1) && confirm.includes(dropA2) && confirm.includes(dropB), "la confirmación lista cada copia que se va");
  assert((await button("Limpiar")).disabled, "Limpiar empieza desactivado");

  // Cancelar no toca nada.
  await click("Cancelar");
  await sleep(300);
  assert(existsSync(dropA1) && existsSync(dropB), "cancelar en la confirmación no borra");

  await click("Limpiar…");
  await waitFor("no cabe en tu papelera");
  await shot("d4b-no-cabe");
  await evaluate(`document.querySelector('[role=dialog] input[type=checkbox]').click()`);
  await sleep(200);
  assert((await button("Limpiar")).disabled, "si no cabe en la papelera, la revisión no activa Limpiar");

  // Recortar deja el grupo más grande que cabe: 6 MB de playa, y 2 MB de sol ya no.
  await click("Dejar solo lo que cabe");
  await sleep(300);
  const trimmed = await text();
  assert(!trimmed.includes("no cabe en tu papelera"), "el aviso desaparece al recortar");
  assert(trimmed.includes(dropA1) && !trimmed.includes(`A la papelera: ${dropB}`), "el recorte quita el grupo que no cabe");
  assert(!(await button("Limpiar")).disabled, "con lo que cabe se activa Limpiar");
  await click("Limpiar");
  await waitFor("Limpieza terminada", 30000);
  await sleep(300);
  await shot("d5-resultado");
  const result = await text();
  console.log(result.slice(result.indexOf("Limpieza terminada"), result.indexOf("Limpieza terminada") + 200));

  assert(!existsSync(dropA1) && !existsSync(dropA2), "las copias que caben salieron de su lugar");
  assert(existsSync(dropB), "lo que no cabía sigue intacto");
  assert(readFileSync(keepA).length === 3 * MB && readFileSync(keepA2).length === 3 * MB && readFileSync(keepB).length === 2 * MB, "las conservadas siguen completas, incluidas las dos de la misma carpeta");
  assert(existsSync(untouchedA) && existsSync(untouchedB), "el grupo sin copia en esa carpeta sigue intacto");
  assert(existsSync(lookalike), "el parecido sigue intacto");
  assert(result.includes("En la papelera"), "dice cuánto quedó en la papelera");

  await click("Cerrar");
  await waitFor("Puedes recuperar hasta");
  await sleep(300);
  await shot("d6-tras-limpiar");
  // Fototeca conserva dos copias de playa, así que ese grupo sigue siendo un duplicado.
  const left = await text();
  assert(left.includes("en 3 grupos") && !left.includes(dropA1), "las copias enviadas salen de la lista y los grupos con copias siguen");

  // Inglés: comprobar textos en la pantalla principal de la función.
  await click("EN");
  await waitFor("You can recover up to");
  await shot("d7-ingles");
  await click("ES");
  await waitFor("Puedes recuperar hasta");

  assert(errors.length === 0, `sin errores de consola: ${errors}`);
  ws.close();

  restoreFromBin();
  await sleep(1000);
  assert(existsSync(dropA1) && existsSync(dropA2), "las copias se restauraron desde la papelera");
} catch (err) {
  console.error("FALLO:", err.message);
  code = 1;
  try { restoreFromBin(); } catch {}
} finally {
  app.kill();
  await sleep(500);
  try { rmSync(root, { recursive: true, force: true }); } catch {}
}
process.exit(code);
