// HackBrazil demo recorder (KLOX-269): one muted video per scene,
// PT-BR, with an injected virtual cursor. The default target is the
// deterministic local demo; the script refuses live-mail/production.
//
// Run from frontend/:
//   DATABASE_URL=postgres://… node scripts/record-videos.mjs
//   node scripts/record-videos.mjs --scene 6 --base-url http://localhost:8137
//
// DATABASE_URL is required only when the script has to start the local
// demo server itself. MP4 conversion uses ffmpeg when it is on PATH;
// otherwise the .webm originals are kept and reported.
import { chromium } from "@playwright/test";
import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const frontendDir = path.resolve(scriptDir, "..");
const repoRoot = path.resolve(frontendDir, "..");

const args = parseArgs(process.argv.slice(2));
const baseURL = stripSlash(args["base-url"] ?? process.env.KLOXUS_RECORD_BASE_URL ?? "http://localhost:8137");
const outDir = path.resolve(args["out-dir"] ?? process.env.KLOXUS_RECORD_OUT ?? path.join(frontendDir, "recordings", "hackbrazil"));
const rawDir = path.join(outDir, "webm");
const sceneArg = args.scene ?? "all";
const fast = args.fast === true || process.env.KLOXUS_RECORD_FAST === "1";
const keepWebm = args["keep-webm"] === true || process.env.KLOXUS_RECORD_KEEP_WEBM === "1";
const email = process.env.KLOXUS_RECORD_EMAIL ?? "criador@exemplo.com";
const viewport = { width: 1920, height: 1080 };

const SCENES = [
  { n: 1, slug: "hook", targetMs: 8_000 },
  { n: 2, slug: "problema", targetMs: 12_000 },
  { n: 3, slug: "cadastro-magic-link", targetMs: 15_000, mutating: true },
  { n: 4, slug: "radar-match", targetMs: 15_000, mutating: true },
  { n: 5, slug: "hoje-montar-pauta", targetMs: 30_000, mutating: true },
  { n: 6, slug: "escrever", targetMs: 40_000, mutating: true },
  { n: 7, slug: "exportar-copiado", targetMs: 25_000, mutating: true },
  { n: 8, slug: "cta", targetMs: 15_000 },
];

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (!a.startsWith("--")) continue;
    const key = a.slice(2);
    if (key === "fast" || key === "keep-webm" || key === "no-server") {
      out[key] = true;
    } else {
      out[key] = argv[++i];
    }
  }
  return out;
}

function stripSlash(u) {
  return u.replace(/\/+$/, "");
}

function ensureDir(dir) {
  fs.mkdirSync(dir, { recursive: true });
}

function commandExists(cmd) {
  const probe = spawnSync(cmd, ["-version"], { stdio: "ignore" });
  return probe.status === 0;
}

async function health(base) {
  try {
    const res = await fetch(`${base}/healthz`, { signal: AbortSignal.timeout(2_000) });
    return res.ok;
  } catch {
    return false;
  }
}

async function waitHealthy(base, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await health(base)) return;
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`demo server did not become healthy at ${base}`);
}

function startServerIfNeeded(base) {
  const u = new URL(base);
  const loopback = ["localhost", "127.0.0.1", "::1"].includes(u.hostname);
  if (!loopback || args["no-server"]) return null;
  if (!process.env.DATABASE_URL) {
    throw new Error(
      "DATABASE_URL is not set. Start a disposable Postgres and export it, or start the demo server yourself and pass --no-server.",
    );
  }
  const port = u.port || "8137";
  const env = {
    ...process.env,
    KLOXUS_PORT: port,
    KLOXUS_DATA_DIR: process.env.KLOXUS_DATA_DIR ?? path.join(repoRoot, ".rec-data", "video"),
    KLOXUS_NO_BROWSER: "1",
    KLOXUS_NO_DOTENV: "1",
    KLOXUS_LLM_MODE: "off",
    KLOXUS_PUBLISHING: process.env.KLOXUS_PUBLISHING ?? "on",
    RESEND_API_KEY: "",
    RESEND_BASE_URL: "",
    RESEND_WEBHOOK_SECRET: "",
    KLOXUS_FAKE_RESEND_URL: "",
    ANTHROPIC_API_KEY: "",
    FIRECRAWL_API_KEY: "",
    ABACATEPAY_API_KEY: "",
    VOYAGE_API_SECRET: "",
  };
  const child = spawn("go", ["run", "../cmd/kloxus", "demo"], {
    cwd: frontendDir,
    env,
    stdio: ["ignore", "pipe", "pipe"],
    detached: process.platform !== "win32",
  });
  let tail = "";
  const remember = (chunk) => {
    tail = (tail + chunk.toString()).slice(-4_000);
  };
  child.stdout.on("data", remember);
  child.stderr.on("data", remember);
  child.on("exit", (code) => {
    if (code !== null && code !== 0) console.error(`demo server exited with ${code}\n${tail}`);
  });
  return child;
}

function stopServer(child) {
  if (!child || child.killed) return;
  try {
    if (process.platform === "win32") {
      spawnSync("taskkill", ["/pid", String(child.pid), "/T", "/F"], { stdio: "ignore" });
    } else {
      process.kill(-child.pid, "SIGTERM");
    }
  } catch {
    try { child.kill(); } catch (err) {
      if (process.env.KLOXUS_RECORD_DEBUG) console.error("server stop failed", err);
    }
  }
}

const CURSOR_INIT = `(() => {
  const install = () => {
    if (document.getElementById('__kx-cursor')) return;
    const el = document.createElement('div');
    el.id = '__kx-cursor';
    el.innerHTML = '<div class="__kx-ring"></div><svg class="__kx-arrow" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 3l13.5 8.4-6.2 1.6-3.2 5.7L5 3z" fill="white" stroke="#111814" stroke-width="1.7" stroke-linejoin="round"/></svg>';
    const style = document.createElement('style');
    style.textContent = '#__kx-cursor{position:fixed;left:0;top:0;width:24px;height:24px;z-index:2147483647;pointer-events:none;transform:translate3d(50vw,50vh,0);transition:filter .12s ease}#__kx-cursor .__kx-arrow{position:absolute;inset:0;filter:drop-shadow(0 2px 4px rgba(0,0,0,.35))}#__kx-cursor .__kx-ring{position:absolute;left:-10px;top:-10px;width:34px;height:34px;border:2px solid rgba(14,59,46,.55);border-radius:50%;transform:scale(.55);opacity:0}#__kx-cursor.__kx-down .__kx-ring{animation:__kx-pulse .38s ease-out}#__kx-cursor.__kx-down .__kx-arrow{filter:drop-shadow(0 1px 2px rgba(0,0,0,.45)) brightness(.92)}@keyframes __kx-pulse{0%{opacity:.8;transform:scale(.45)}100%{opacity:0;transform:scale(1.35)}}';
    document.documentElement.appendChild(style);
    document.documentElement.appendChild(el);
    let pos = { x: window.innerWidth / 2, y: window.innerHeight / 2 };
    window.__kxCursorPos = pos;
    const move = (e) => {
      pos = { x: e.clientX, y: e.clientY };
      window.__kxCursorPos = pos;
      el.style.transform = 'translate3d(' + pos.x + 'px,' + pos.y + 'px,0)';
    };
    const down = () => {
      el.classList.remove('__kx-down');
      void el.offsetWidth;
      el.classList.add('__kx-down');
    };
    const up = () => el.classList.remove('__kx-down');
    for (const type of ['pointermove', 'mousemove']) document.addEventListener(type, move, true);
    document.addEventListener('pointerdown', down, true);
    document.addEventListener('pointerup', up, true);
    document.addEventListener('mouseleave', up, true);
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', install, { once: true });
  else install();
})();`;

function ease(t) {
  return t < 0.5 ? 2 * t * t : -1 + (4 - 2 * t) * t;
}

async function cursorPos(page) {
  return await page.evaluate(() => window.__kxCursorPos ?? { x: window.innerWidth / 2, y: window.innerHeight / 2 });
}

async function humanMove(page, x, y, fastMode = false) {
  const start = await cursorPos(page);
  const dx = x - start.x;
  const dy = y - start.y;
  const dist = Math.hypot(dx, dy);
  const steps = fastMode ? 4 : Math.max(10, Math.min(34, Math.round(dist / 28)));
  const pause = fastMode ? 4 : Math.max(9, Math.min(22, Math.round(dist / 45)));
  for (let i = 1; i <= steps; i++) {
    const t = ease(i / steps);
    const bend = Math.sin((i / steps) * Math.PI) * Math.min(18, dist / 12);
    const px = start.x + dx * t - (dy / Math.max(dist, 1)) * bend;
    const py = start.y + dy * t + (dx / Math.max(dist, 1)) * bend;
    await page.mouse.move(px, py);
    await page.waitForTimeout(pause);
  }
  await page.mouse.move(x, y);
}

async function pointFor(locator) {
  await locator.scrollIntoViewIfNeeded();
  const box = await locator.boundingBox();
  if (!box) throw new Error("locator has no visible box");
  return {
    x: box.x + box.width * 0.52,
    y: box.y + box.height * 0.52,
  };
}

async function hover(page, locator, ui) {
  await locator.waitFor({ state: "visible", timeout: 30_000 });
  const p = await pointFor(locator);
  await humanMove(page, p.x, p.y, ui.fastMode);
  await ui.pause(260);
}

async function click(page, locator, ui, pauseMs = 380) {
  await hover(page, locator, ui);
  await ui.pause(120);
  await locator.click();
  await ui.pause(pauseMs);
}

async function typeText(page, locator, text, ui) {
  await click(page, locator, ui, 120);
  await locator.pressSequentially(text, { delay: ui.fastMode ? 4 : ui.record ? 62 : 12 });
  await ui.pause(260);
}

class UI {
  constructor(record, fastMode) {
    this.record = record;
    this.fastMode = fastMode || !record;
  }
  pause(ms) {
    const scale = this.fastMode ? 0.18 : 1;
    return this.page.waitForTimeout(Math.max(30, Math.round(ms * scale)));
  }
}

class RunState {
  constructor() {
    this.storageState = undefined;
    this.pubID = null;
    this.issueID = null;
  }
}

async function newContext(browser, state, record) {
  const context = await browser.newContext({
    baseURL,
    locale: "pt-BR",
    viewport,
    deviceScaleFactor: 1,
    permissions: ["clipboard-read", "clipboard-write"],
    recordVideo: record ? { dir: rawDir, size: viewport } : undefined,
    ...(state.storageState ? { storageState: state.storageState } : {}),
  });
  await context.addInitScript(CURSOR_INIT);
  const page = await context.newPage();
  page.setDefaultTimeout(30_000);
  const ui = new UI(record, fast);
  ui.page = page;
  return { context, page, ui };
}

async function closeContext(context, page, state, scene, record, results) {
  state.storageState = await context.storageState();
  const video = record ? page.video() : null;
  await context.close();
  if (!video) return;
  const base = `cena-${scene.n}-${scene.slug}`;
  const webm = path.join(rawDir, `${base}.webm`);
  await video.saveAs(webm);
  const item = { scene: scene.n, slug: scene.slug, webm: path.basename(webm), mp4: null, seconds: null };
  const mp4 = path.join(outDir, `${base}.mp4`);
  if (commandExists("ffmpeg")) {
    const conv = spawnSync("ffmpeg", [
      "-y", "-hide_banner", "-loglevel", "error",
      "-i", webm,
      "-c:v", "libx264", "-preset", "medium", "-crf", "20",
      "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-an", mp4,
    ], { encoding: "utf8" });
    if (conv.status === 0) {
      item.mp4 = path.basename(mp4);
      if (!keepWebm) fs.rmSync(webm, { force: true });
      if (commandExists("ffprobe")) {
        const probe = spawnSync("ffprobe", ["-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", mp4], { encoding: "utf8" });
        const seconds = Number.parseFloat((probe.stdout || "").trim());
        if (Number.isFinite(seconds)) item.seconds = Math.round(seconds * 10) / 10;
      }
    } else {
      item.conversion_error = (conv.stderr || conv.stdout || "").trim().slice(-500);
    }
  } else {
    item.conversion_error = "ffmpeg not found on PATH";
  }
  results.push(item);
}

async function finishScene(ui, scene, startedAt) {
  if (!ui.record || fast) return;
  const remaining = scene.targetMs - (Date.now() - startedAt);
  if (remaining > 0) await ui.page.waitForTimeout(remaining);
}

async function fetchJSON(page, url) {
  return await page.evaluate(async (u) => {
    const res = await fetch(u);
    if (!res.ok) throw new Error(`${u}: ${res.status}`);
    return res.json();
  }, url);
}

async function scene1(page, ui) {
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: /A pauta pronta\. A\s*escrita é sua\./ }).waitFor();
  await humanMove(page, viewport.width * 0.72, viewport.height * 0.42, ui.fastMode);
  await ui.pause(2_000);
}

async function scene2(page, ui) {
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: /A pauta pronta\. A\s*escrita é sua\./ }).waitFor();
  await ui.pause(2_000);
  await page.mouse.wheel(0, 240);
  await ui.pause(2_500);
  await page.mouse.wheel(0, -240);
  await ui.pause(1_500);
}

async function scene3(page, ui) {
  await page.goto("/", { waitUntil: "domcontentloaded" });
  const cta = page.getByRole("button", { name: /Criar meu Radar/ }).first();
  await click(page, cta, ui, 500);
  await page.waitForURL(/\/start/);
  await page.getByRole("heading", { name: "Confirme seu e-mail para criar o radar" }).waitFor();
  await typeText(page, page.getByLabel(/você@exemplo\.com/), email, ui);
  await click(page, page.getByRole("button", { name: "Enviar link" }), ui, 500);
  const open = page.getByRole("link", { name: "Abrir o link" });
  await open.waitFor({ timeout: 15_000 });
  await ui.pause(900);
  await click(page, open, ui, 500);
  const confirm = page.getByRole("button", { name: "Confirmar e continuar" });
  await confirm.waitFor();
  await ui.pause(800);
  await click(page, confirm, ui, 500);
  await page.waitForURL(/\/start/, { timeout: 15_000 });
  await page.getByLabel("Nome do Radar").waitFor({ timeout: 20_000 });
}

async function scene4(page, ui, state) {
  await page.goto("/start", { waitUntil: "domcontentloaded" });
  const name = page.getByLabel("Nome do Radar");
  await name.waitFor({ timeout: 20_000 });
  await typeText(page, name, "Fintech para bancários", ui);
  await typeText(page, page.getByLabel(/Temas/), "Pix, open finance, Drex, regulação do BC", ui);
  const group = page.getByRole("group", { name: "Editorias (opcional)" });
  for (const re of [/Economia/, /Negócios/, /Tecnologia/]) {
    await hover(page, group.getByRole("button", { name: re }).first(), ui);
  }
  await click(page, group.getByRole("button", { name: /Mercado financeiro/ }).first(), ui, 220);
  await click(page, group.getByRole("button", { name: /Startups/ }).first(), ui, 260);
  await click(page, page.getByRole("button", { name: "Continuar", exact: true }), ui, 500);
  await page.getByRole("heading", { name: "Dar match", exact: true }).waitFor({ timeout: 30_000 });
  const card = page.getByTestId("calibration-card");
  await card.waitFor({ timeout: 60_000 });
  await ui.pause(900);
  await click(page, card.getByRole("button", { name: /Sim/ }), ui, 650);
  const skip = page.getByRole("button", { name: "Pular", exact: true });
  await click(page, skip, ui, 500);
  await page.getByRole("heading", { name: "Pronto!", exact: true }).waitFor({ timeout: 30_000 });
  await page.getByText(/notícias combinam com seu Radar hoje/).waitFor({ timeout: 30_000 });
  await ui.pause(1_200);
  const pubs = await fetchJSON(page, "/api/v1/publications");
  state.pubID = pubs.publications?.[0]?.id ?? null;
  if (!state.pubID) throw new Error("Radar was created but no publication id was returned");
}

async function scene5(page, ui, state) {
  if (!state.pubID) throw new Error("scene5 needs pubID");
  await page.goto(`/publications/${state.pubID}/today`, { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "Notícias de hoje" }).waitFor();
  const cards = page.getByTestId("today-card");
  await cards.first().waitFor({ timeout: 30_000 });
  await ui.pause(1_000);
  for (const i of [0, 1, 2]) {
    await click(page, cards.nth(i), ui, 520);
  }
  await page.getByText("3 notícias selecionadas").waitFor();
  await ui.pause(1_300);
  await click(page, page.getByRole("button", { name: "Montar pauta →" }), ui, 500);
  await page.getByText("Juntando as 3 notícias…").waitFor({ timeout: 5_000 }).catch(() => {});
  await page.waitForURL(/\/pauta\/\d+$/, { timeout: 60_000 });
  await page.getByTestId("pauta-entry").first().waitFor({ timeout: 30_000 });
  state.issueID = Number(page.url().match(/\/pauta\/(\d+)/)?.[1]);
  if (!state.issueID) throw new Error("pauta URL did not carry an issue id");
  await ui.pause(1_000);
}

async function scene6(page, ui, state) {
  if (!state.issueID) throw new Error("scene6 needs issueID");
  await page.goto(`/pauta/${state.issueID}`, { waitUntil: "domcontentloaded" });
  const first = page.getByTestId("pauta-entry").first();
  await first.waitFor({ timeout: 30_000 });
  await ui.pause(900);
  const write = first.getByRole("button", { name: /^Escrever:/ });
  if (await write.count()) await click(page, write, ui, 400);
  const box = first.locator("textarea");
  await box.waitFor();
  const text = "O ponto aqui não é só a novidade regulatória: é a corrida operacional dos bancos para adaptar integrações sem travar a experiência do cliente.";
  await typeText(page, box, text, ui);
  await page.getByText("Salvo", { exact: true }).first().waitFor({ timeout: 10_000 });
  await ui.pause(1_600);
  await hover(page, page.getByRole("button", { name: "+ Adicionar notícia" }), ui);
  await ui.pause(800);
}

async function scene7(page, ui, state) {
  if (!state.issueID) throw new Error("scene7 needs issueID");
  await page.goto(`/pauta/${state.issueID}/export`, { waitUntil: "domcontentloaded" });
  await page.getByText("Para onde vai?").waitFor({ timeout: 30_000 });
  await ui.pause(700);
  const formats = [/Post LinkedIn/, /Post X/, /Newsletter \/ blog \/ intranet/, /Markdown/];
  for (const re of formats) {
    const box = page.getByRole("checkbox", { name: re });
    await click(page, box, ui, 420);
  }
  const linkedinTab = page.getByRole("tab", { name: /Post LinkedIn/ });
  if (await linkedinTab.count()) await click(page, linkedinTab, ui, 300);
  const copy = page.getByRole("button", { name: "Copiar", exact: true }).first();
  await copy.waitFor({ timeout: 15_000 });
  await ui.pause(500);
  await click(page, copy, ui, 400);
  await page.getByRole("button", { name: "Copiado" }).first().waitFor({ timeout: 15_000 });
  await ui.pause(1_500);
}

async function scene8(page, ui, state) {
  if (!state.issueID) throw new Error("scene8 needs issueID");
  await page.goto(`/pauta/${state.issueID}/export`, { waitUntil: "domcontentloaded" });
  await page.getByText("Para onde vai?").waitFor({ timeout: 30_000 });
  await humanMove(page, viewport.width * 0.84, viewport.height * 0.78, ui.fastMode);
  await ui.pause(800);
  await page.evaluate((url) => {
    const old = document.getElementById("__kx-endcard");
    old?.remove();
    const el = document.createElement("div");
    el.id = "__kx-endcard";
    el.textContent = url;
    el.style.cssText = "position:fixed;left:50%;bottom:7vh;transform:translateX(-50%);z-index:2147483646;padding:.85rem 1.5rem;border-radius:999px;background:#0e3b2e;color:white;font:700 28px system-ui,-apple-system,'Segoe UI',sans-serif;letter-spacing:.01em;box-shadow:0 14px 40px rgba(0,0,0,.25);pointer-events:none";
    document.documentElement.appendChild(el);
  }, "kloxus.fly.dev");
  await ui.pause(4_000);
}

const RUNNERS = {
  1: scene1,
  2: scene2,
  3: scene3,
  4: scene4,
  5: scene5,
  6: scene6,
  7: scene7,
  8: scene8,
};

async function preflight() {
  const res = await fetch(`${baseURL}/api/v1/workspace`, { signal: AbortSignal.timeout(10_000) });
  if (!res.ok) throw new Error(`/api/v1/workspace returned ${res.status}`);
  const ws = await res.json();
  if (ws.mode !== "demo" || ws.mail !== "outbox" || ws.mail_transport !== "outbox") {
    throw new Error(
      `refusing to record against mode=${ws.mode} mail=${ws.mail} transport=${ws.mail_transport}; use the deterministic local demo (outbox only)`,
    );
  }
}

function selectedScenes() {
  if (sceneArg === "all") return SCENES;
  const n = Number(sceneArg);
  const scene = SCENES.find((s) => s.n === n);
  if (!scene) throw new Error(`--scene must be all or one of ${SCENES.map((s) => s.n).join(", ")}`);
  return [scene];
}

async function main() {
  ensureDir(outDir);
  ensureDir(rawDir);
  let server = null;
  if (!(await health(baseURL))) server = startServerIfNeeded(baseURL);
  try {
    await waitHealthy(baseURL, 90_000);
    await preflight();

    const browser = await chromium.launch({ headless: true });
    const state = new RunState();
    const results = [];
    const wanted = selectedScenes();
    const wantedSet = new Set(wanted.map((s) => s.n));
    const lastWanted = Math.max(...wanted.map((s) => s.n));
    const plan = sceneArg === "all"
      ? SCENES
      : SCENES.filter((s) => s.mutating && s.n < lastWanted).concat(wanted);

    for (const scene of plan) {
      const record = wantedSet.has(scene.n);
      const { context, page, ui } = await newContext(browser, state, record);
      const startedAt = Date.now();
      try {
        await RUNNERS[scene.n](page, ui, state);
        await finishScene(ui, scene, startedAt);
      } finally {
        await closeContext(context, page, state, scene, record, results);
      }
      console.log(`${record ? "recorded" : "prepared"} scene ${scene.n} (${scene.slug})`);
    }

    await browser.close();
    const total = results.reduce((acc, r) => acc + (r.seconds ?? 0), 0);
    const manifest = {
      generated_at: new Date().toISOString(),
      base_url: baseURL,
      locale: "pt-BR",
      viewport,
      email,
      muted: true,
      total_seconds: Math.round(total * 10) / 10,
      scenes: results,
    };
    fs.writeFileSync(path.join(outDir, "manifest.json"), JSON.stringify(manifest, null, 2) + "\n");
    console.log(`videos written to ${outDir}`);
    console.log(`total recorded duration: ${manifest.total_seconds}s`);
    if (results.some((r) => !r.mp4)) {
      console.log("warning: at least one scene has no MP4; install ffmpeg or convert the .webm files");
    }
  } finally {
    stopServer(server);
  }
}

await main();
