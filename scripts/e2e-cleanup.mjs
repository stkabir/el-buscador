// Prueba la limpieza en la app real, escaneando solo una carpeta temporal.
// Uso: node scripts/e2e-cleanup.mjs <carpeta de capturas> <e2e.exe>
import { spawn, execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const [dir, exe] = process.argv.slice(2);
const port = 9333;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const MB2 = Buffer.alloc(2 * 1024 * 1024);

const root = mkdtempSync(join(tmpdir(), "el-buscador-e2e-"));
const put = (rel, content) => {
  const path = join(root, rel);
  mkdirSync(join(path, ".."), { recursive: true });
  writeFileSync(path, content);
  return path;
};
const manifest = JSON.stringify({ name: "demo" });
// Seguro: dependencias con archivo de versiones.
put("seguro/package.json", manifest);
put("seguro/package-lock.json", "{}");
const safeFile = put("seguro/node_modules/pkg/index.js", MB2);
// Revisar: salida de compilación sin nada que garantice recrearla.
put("revisar/package.json", manifest);
const reviewFile = put("revisar/dist/app.js", MB2);
// Cambia tras el escaneo: debe rechazarse aunque se haya seleccionado.
put("cambia/package.json", manifest);
put("cambia/package-lock.json", "{}");
const changing = join(root, "cambia", "node_modules");
put("cambia/node_modules/pkg/index.js", MB2);

const app = spawn(exe, [], { stdio: "ignore", env: { ...process.env, EL_BUSCADOR_E2E_ROOT: root } });

const restoreFromBin = () => {
  // Saca de la papelera solo lo que esta prueba mandó, y limpia en temp.
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
    const ok = await evaluate(`(() => { const b = [...document.querySelectorAll('button')].filter(x => x.innerText.trim().startsWith(${JSON.stringify(label)})).pop(); if (!b) return false; b.click(); return true; })()`);
    if (!ok) throw new Error(`Sin botón: ${label}. Pantalla: ${(await text()).slice(0, 400)}`);
  };
  const waitFor = async (needle, ms = 15000) => {
    const start = Date.now();
    while (Date.now() - start < ms) {
      if ((await text()).includes(needle)) return;
      await sleep(100);
    }
    throw new Error(`Timeout: ${needle}. Pantalla: ${(await text()).slice(0, 500)}`);
  };
  const shot = async (name) => {
    const { result } = await send("Page.captureScreenshot", { format: "png" });
    writeFileSync(`${dir}/${name}.png`, Buffer.from(result.data, "base64"));
  };
  const check = async (path) => {
    const ok = await evaluate(`(() => { const b = document.querySelector(${JSON.stringify(`input[aria-label="Seleccionar ${path.replace(/\\/g, "\\\\")}"]`)}); if (!b) return false; b.click(); return true; })()`);
    if (!ok) throw new Error(`Sin casilla: ${path}`);
  };
  const assert = (condition, message) => {
    if (!condition) throw new Error(`Falló: ${message}`);
    console.log(`  ok: ${message}`);
  };
  const button = (label) => evaluate(`(() => { const b = [...document.querySelectorAll('button')].filter(x => x.innerText.trim() === ${JSON.stringify(label)}).pop(); return b ? { disabled: b.disabled } : null; })()`);

  await send("Runtime.enable");
  await send("Page.enable");
  await waitFor("libres de");
  await click("Ver lo que más pesa");
  await waitFor("Encontramos", 60000);
  await click("Basura dev");
  await waitFor("Proyectos");
  await sleep(300);
  await shot("c1-basura-dev");

  const safeDir = join(root, "seguro", "node_modules");
  const reviewDir = join(root, "revisar", "dist");

  // Seleccionar y cancelar no toca nada.
  await check(safeDir);
  await waitFor("1 seleccionados");
  await click("Limpiar…");
  await waitFor("¿Limpiar");
  await shot("c2-confirmar");
  await click("Cancelar");
  await sleep(300);
  assert(existsSync(safeFile), "cancelar en la confirmación no borra");

  // Revisar exige la confirmación adicional.
  await check(reviewDir);
  await check(changing);
  await waitFor("3 seleccionados");
  await click("Limpiar…");
  await waitFor("Necesit", 300).catch(() => {});
  await waitFor("Revisé los elementos");
  await shot("c3-confirmar-revisar");
  assert((await button("Limpiar")).disabled, "con Revisar, Limpiar empieza desactivado");
  await evaluate(`document.querySelector('[role=dialog] input[type=checkbox]').click()`);
  await sleep(200);
  assert(!(await button("Limpiar")).disabled, "marcar la revisión activa Limpiar");

  // El semáforo se revalida al ejecutar: se quita el marcador del proyecto.
  rmSync(join(root, "cambia", "package.json"));
  rmSync(join(root, "cambia", "package-lock.json"));

  await click("Limpiar");
  await waitFor("Limpieza terminada", 30000);
  await sleep(300);
  await shot("c4-resultado");
  const result = await text();
  console.log(result.slice(result.indexOf("Limpieza terminada"), result.indexOf("Limpieza terminada") + 500));

  assert(!existsSync(safeFile), "Seguro salió de su lugar");
  assert(!existsSync(reviewFile), "Revisar confirmado salió de su lugar");
  assert(existsSync(join(changing, "pkg", "index.js")), "lo que cambió tras el escaneo sigue intacto");
  assert(result.includes("En la papelera"), "dice cuánto quedó en la papelera");
  assert(result.includes("Ya no sabemos"), "explica por qué no limpió lo que cambió");

  await click("Cerrar");
  await sleep(300);
  assert(!(await text()).includes("seguro\\node_modules"), "lo limpiado desaparece de la lista");
  assert(errors.length === 0, `sin errores de consola: ${errors}`);
  ws.close();

  restoreFromBin();
  await sleep(1000);
  assert(existsSync(safeFile), "Seguro se restauró desde la papelera");
  assert(existsSync(reviewFile), "Revisar se restauró desde la papelera");
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
