// Prueba el actualizador de punta a punta, sin GitHub: una versión 0.0.1 en una carpeta
// temporal se actualiza a la 0.0.2 desde un servidor local, con llave de prueba.
// Uso: node scripts/e2e-update.mjs <carpeta de capturas>
import { execFileSync, spawn } from "node:child_process";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { copyFileSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const dir = process.argv[2];
const port = 9333;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const repo = "stkabir/el-buscador";
const asset = "el-buscador.exe";

const root = mkdtempSync(join(tmpdir(), "el-buscador-e2e-update-"));
const build = (version, out) =>
  execFileSync("go", ["build", "-tags", "e2e", "-ldflags", `-X main.version=${version}`, "-o", out, "."], { stdio: "inherit" });
const installed = join(root, "app", asset);
const newer = join(root, "nueva.exe");
execFileSync("cmd", ["/c", "mkdir", join(root, "app")]);
console.log("Compilando dos versiones…");
build("0.0.1", installed);
build("0.0.2", newer);

const hash = (path) => createHash("sha256").update(readFileSync(path)).digest();
const oldHash = hash(installed).toString("hex");
const newBytes = readFileSync(newer);
const newDigest = createHash("sha256").update(newBytes).digest();

const { publicKey, privateKey } = generateKeyPairSync("ed25519");
const publicHex = Buffer.from(publicKey.export({ format: "jwk" }).x, "base64url").toString("hex");
const attacker = generateKeyPairSync("ed25519").privateKey;

// Primero sirve una firma hecha con otra llave, como haría quien tomó la cuenta.
let signature = sign(null, newDigest, attacker);

const server = createServer((req, res) => {
  const base = `http://127.0.0.1:${server.address().port}`;
  const prefix = `/${repo}/releases/download/v0.0.2/`;
  if (req.url === `/repos/${repo}/releases/latest`) {
    const assets = [asset, "checksums.txt", `${asset}.sig`].map((name) => {
      const url = `${base}${prefix}${name}`;
      return { name, size: name === asset ? newBytes.length : 64, url, browser_download_url: url };
    });
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ tag_name: "v0.0.2", assets }));
  } else if (req.url === `${prefix}${asset}`) {
    res.end(newBytes);
  } else if (req.url === `${prefix}checksums.txt`) {
    res.end(`${newDigest.toString("hex")}  ${asset}\n`);
  } else if (req.url === `${prefix}${asset}.sig`) {
    res.end(signature);
  } else {
    res.statusCode = 404;
    res.end();
  }
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));

const env = {
  ...process.env,
  EL_BUSCADOR_E2E_UPDATE_API: `http://127.0.0.1:${server.address().port}`,
  EL_BUSCADOR_E2E_UPDATE_KEY: publicHex,
};
const app = spawn(resolve(installed), [], { stdio: "ignore", env, cwd: root });

const connect = async () => {
  let page;
  for (let i = 0; i < 60 && !page; i++) {
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
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); }
  };
  const send = (method, params = {}) => new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method, params })); });
  const text = async () => (await send("Runtime.evaluate", { expression: "document.body.innerText", returnByValue: true })).result?.result?.value ?? "";
  return { ws, send, text };
};
const waitFor = async (page, needle, ms = 30000) => {
  const start = Date.now();
  while (Date.now() - start < ms) {
    if ((await page.text()).includes(needle)) return;
    await sleep(150);
  }
  throw new Error(`Timeout: ${needle}. Pantalla: ${(await page.text()).slice(0, 400)}`);
};
const click = async (page, label) => {
  const expression = `(() => { const b = [...document.querySelectorAll('button')].find(x => x.innerText.trim().startsWith(${JSON.stringify(label)})); if (!b) return false; b.click(); return true; })()`;
  const ok = (await page.send("Runtime.evaluate", { expression, returnByValue: true })).result?.result?.value;
  if (!ok) throw new Error(`Sin botón: ${label}. Pantalla: ${(await page.text()).slice(0, 400)}`);
};
const shot = async (page, name) => {
  const { result } = await page.send("Page.captureScreenshot", { format: "png" });
  writeFileSync(`${dir}/${name}.png`, Buffer.from(result.data, "base64"));
};
const assert = (condition, message) => {
  if (!condition) throw new Error(`Falló: ${message}`);
  console.log(`  ok: ${message}`);
};

let code = 0;
try {
  let page = await connect();
  await waitFor(page, "Versión 0.0.1");
  await waitFor(page, "Hay una versión nueva: 0.0.2");
  await shot(page, "u1-disponible");
  assert(hash(installed).toString("hex") === oldHash, "avisar no instala nada");

  // Firma falsa: se rechaza y la app sigue intacta.
  await click(page, "Actualizar");
  await waitFor(page, "No pudimos actualizar");
  await sleep(300);
  await shot(page, "u2-firma-falsa");
  assert(hash(installed).toString("hex") === oldHash, "con firma falsa, el programa no cambia");
  assert((await page.text()).includes("Versión 0.0.1"), "con firma falsa, sigue en la misma versión");
  page.ws.close();

  // Ya con la firma buena: al volver a abrir la detecta, y con un clic se actualiza.
  signature = sign(null, newDigest, privateKey);
  execFileSync("taskkill", ["/F", "/IM", asset], { stdio: "ignore" });
  await sleep(1000);
  spawn(resolve(installed), [], { stdio: "ignore", env, cwd: root, detached: true }).unref();
  page = await connect();
  await waitFor(page, "Hay una versión nueva: 0.0.2");
  await click(page, "Actualizar");
  page.ws.close();

  // La app se cierra y vuelve a abrir sola.
  let replaced = false;
  for (let i = 0; i < 80 && !replaced; i++) {
    await sleep(500);
    try { replaced = hash(installed).equals(newDigest); } catch {}
  }
  assert(replaced, "el programa quedó reemplazado por la versión firmada");
  await sleep(1500);
  page = await connect();
  await waitFor(page, "Versión 0.0.2");
  await sleep(300);
  await shot(page, "u3-actualizada");
  assert(!(await page.text()).includes("Hay una versión nueva"), "tras actualizar, ya no ofrece otra");
  page.ws.close();
} catch (err) {
  console.error("FALLO:", err.message);
  code = 1;
} finally {
  try { execFileSync("taskkill", ["/F", "/IM", asset], { stdio: "ignore" }); } catch {}
  server.close();
  await sleep(800);
  try { rmSync(root, { recursive: true, force: true }); } catch {}
}
process.exit(code);
