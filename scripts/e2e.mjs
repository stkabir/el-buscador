import { spawn } from "node:child_process";
import { writeFileSync } from "node:fs";

const dir = process.argv[2];
const port = 9333;
const app = spawn(process.argv[3], [], { stdio: "ignore" });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

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
    const ok = await evaluate(`(() => { const b = [...document.querySelectorAll('button')].find(x => x.innerText.trim().startsWith(${JSON.stringify(label)})); if (!b) return false; b.click(); return true; })()`);
    if (!ok) throw new Error(`Sin botón: ${label}. Pantalla: ${(await text()).slice(0, 300)}`);
  };
  const waitFor = async (needle, ms) => {
    const start = Date.now();
    while (Date.now() - start < ms) {
      if ((await text()).includes(needle)) return Date.now() - start;
      await sleep(100);
    }
    throw new Error(`Timeout: ${needle}. Pantalla: ${(await text()).slice(0, 300)}`);
  };
  const shot = async (name) => {
    const { result } = await send("Page.captureScreenshot", { format: "png" });
    writeFileSync(`${dir}/${name}.png`, Buffer.from(result.data, "base64"));
  };

  await send("Runtime.enable");
  await send("Page.enable");
  await waitFor("libres de", 10000);
  await shot("1-inicio");

  await click("Ver lo que más pesa");
  await waitFor("Revisando tu disco", 5000);
  await sleep(1500);
  await shot("2-escaneando");
  console.log("Progreso a 1.5 s:", (await text()).split("\n").slice(0, 3).join(" | "));

  const took = await waitFor("Encontramos", 180000);
  console.log(`Terminó en ${(took / 1000).toFixed(1)} s`);
  await shot("3-archivos");
  console.log("Encabezado:", (await text()).split("\n").slice(0, 4).join(" | "));

  await click("Carpetas");
  await sleep(300);
  await shot("4-carpetas");

  await click("Mapa");
  await waitFor("Sin clasificar", 3000);
  await sleep(400);
  await shot("10-mapa");
  const before = await evaluate("document.querySelectorAll('[data-map-dir]').length");
  const entered = await evaluate("(() => { const b = document.querySelector('[data-map-dir]'); if (!b) return null; const p = b.dataset.mapDir; b.click(); return p; })()");
  if (!before || !entered) throw new Error("El mapa no dibujó carpetas");
  await waitFor(entered, 3000);
  await sleep(400);
  await shot("11-mapa-subcarpeta");
  console.log("Mapa:", before, "carpetas; entró a", entered);
  await click("Subir");
  await sleep(300);

  await click("Carpetas");
  await sleep(300);
  const freeUp = await evaluate("(() => { const b = document.querySelector('[data-free-up]'); if (!b) return null; const p = b.dataset.freeUp; b.click(); return p; })()");
  if (freeUp) {
    await waitFor(freeUp === null ? "" : "Cerrar", 100).catch(() => {});
    const startPreview = Date.now();
    while (Date.now() - startPreview < 60000) {
      const screen = await text();
      if (screen.includes("¿Liberar hasta") || screen.includes("No hay nada que liberar")) break;
      await sleep(200);
    }
    await sleep(200);
    await shot("12-liberar-vista-previa");
    const screen = await text();
    console.log("Liberar espacio, vista previa de", freeUp, ":", screen.slice(screen.search(/¿Liberar hasta|No hay nada que liberar/), screen.search(/¿Liberar hasta|No hay nada que liberar/) + 260).replace(/\n+/g, " | "));
    await click(screen.includes("¿Liberar hasta") ? "Cancelar" : "Cerrar");
    await sleep(200);
  } else {
    console.log("Liberar espacio: sin carpetas sincronizadas en esta lista");
  }

  await click("Basura dev");
  await waitFor("Cachés de herramientas", 3000);
  await sleep(300);
  await shot("8-basura-dev");
  console.log("Basura dev:", (await text()).split("\n").find((line) => line.includes("recuperar") || line.includes("Nada marcado")));

  await click("EN");
  await waitFor("Tool caches", 3000);
  await shot("9-ingles");
  await click("ES");
  await waitFor("Cachés de herramientas", 3000);

  await click("Explorar");
  await waitFor("Subir", 3000);
  await sleep(300);
  await shot("5-explorar");
  const first = await evaluate(`(() => { const b = document.querySelector('ul button'); if (!b) return null; const name = b.innerText.replace('Carpeta: ','').split('\\n')[0]; b.click(); return name; })()`);
  await sleep(400);
  console.log("Entró a:", first);
  await shot("6-subcarpeta");
  await click("Subir");
  await sleep(300);
  const backDisabled = await evaluate(`[...document.querySelectorAll('button')].find(x => x.innerText.trim() === 'Subir').disabled`);
  console.log("En raíz, Subir desactivado:", backDisabled);

  await click("Buscar de nuevo");
  await waitFor("Cancelar", 5000);
  await click("Cancelar");
  await waitFor("Cancelaste", 5000);
  await shot("7-cancelado");
  console.log("Cancelar: correcto");

  console.log("Errores de consola:", errors.length ? errors : "ninguno");
  ws.close();
} catch (err) {
  console.error("FALLO:", err.message);
  code = 1;
} finally {
  app.kill();
}
process.exit(code);
