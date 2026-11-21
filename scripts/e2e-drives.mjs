// Prueba el selector de disco con una unidad subst sobre una carpeta temporal. Sin esto
// haría falta un segundo disco. Como subst es un alias, limpiar ahí debe rechazarse:
// la ruta real no es la que se validó.
// Uso: node scripts/e2e-drives.mjs <carpeta de capturas> <e2e.exe>
import { spawn, execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const [dir, exe] = process.argv.slice(2);
const port = 9333;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const letter = ["X", "Y", "W", "V", "U"].find((l) => !existsSync(`${l}:\\`));
if (!letter) throw new Error("Sin letra libre para subst");
const drive = `${letter}:`;

const root = mkdtempSync(join(tmpdir(), "el-buscador-e2e-drive-"));
const put = (rel, content) => {
  const path = join(root, rel);
  mkdirSync(join(path, ".."), { recursive: true });
  writeFileSync(path, content);
  return path;
};
put("proyecto/package.json", JSON.stringify({ name: "demo" }));
put("proyecto/package-lock.json", "{}");
const file = put("proyecto/node_modules/pkg/index.js", Buffer.alloc(2 * 1024 * 1024));

execFileSync("subst", [drive, root]);
const app = spawn(exe, [], { stdio: "ignore" });

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
    if (!ok) throw new Error(`Sin botón: ${label}. Pantalla: ${(await text()).slice(0, 400)}`);
  };
  const waitFor = async (needle, ms = 30000) => {
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
  const assert = (condition, message) => {
    if (!condition) throw new Error(`Falló: ${message}`);
    console.log(`  ok: ${message}`);
  };

  await send("Runtime.enable");
  await send("Page.enable");
  await waitFor("Otro disco:");
  await sleep(200);
  await shot("e1-selector");
  const start = await text();
  assert(start.includes("Tu disco C:"), "empieza en el disco del sistema");

  await click(drive);
  await waitFor(`Tu disco ${drive}`);
  await shot("e2-otro-disco");
  await click("Ver lo que más pesa");
  await waitFor("Encontramos");
  const header = await text();
  assert(!header.includes("Windows"), "los resultados son solo de ese disco");
  await click("Basura dev");
  await waitFor("node_modules");
  await sleep(200);
  await shot("e3-basura");

  const checked = await evaluate(`(() => { const b = document.querySelector('input[type=checkbox]'); if (!b) return false; b.click(); return true; })()`);
  assert(checked, "ofrece seleccionar lo que encontró");
  await click("Limpiar…");
  await waitFor("Es un enlace");
  await sleep(200);
  await shot("e4-rechazo");
  assert((await text()).includes("Nada de lo que seleccionaste"), "rechaza limpiar en un alias de otra carpeta");
  await click("Cerrar");
  await sleep(200);
  assert(existsSync(file), "no se tocó nada");
  assert(errors.length === 0, `sin errores de consola: ${errors}`);
  ws.close();
} catch (err) {
  console.error("FALLO:", err.message);
  code = 1;
} finally {
  app.kill();
  await sleep(500);
  try { execFileSync("subst", [drive, "/D"]); } catch (err) { console.error("Quita la unidad a mano:", drive, err.message); }
  try { rmSync(root, { recursive: true, force: true }); } catch {}
}
process.exit(code);
