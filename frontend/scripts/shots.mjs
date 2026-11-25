// One-off before/after screenshot capture for the landing rework
// (KLOX-69): PT locale, full-page shots at 1440px and 390px plus the
// legal pages. Run with vite dev on :5199 — the landing renders
// without the API (the publications query failing just means
// "new visitor", the state a fresh browser shows anyway).
import { chromium } from "@playwright/test";

const base = process.env.SHOT_BASE ?? "http://localhost:5199";
const tag = process.env.SHOT_TAG ?? "shot";
const browser = await chromium.launch();

async function shot(width, path, name) {
  const ctx = await browser.newContext({
    viewport: { width, height: 900 },
    locale: "pt-BR",
    deviceScaleFactor: 1,
  });
  const page = await ctx.newPage();
  await page.goto(`${base}${path}`, { waitUntil: "networkidle" });
  // let fonts settle so the serif/sans swap is real in the pixels
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(300);
  await page.screenshot({ path: `../shots/${tag}-${name}-${width}.png`, fullPage: true });
  await ctx.close();
  console.log(`shots: ${tag}-${name}-${width}.png`);
}

for (const w of [1440, 390]) {
  await shot(w, "/", "landing");
}
if (process.env.SHOT_LEGAL === "1") {
  for (const w of [1440, 390]) {
    await shot(w, "/termos", "termos");
    await shot(w, "/privacidade", "privacidade");
  }
}
await browser.close();
