// Records the two review videos against a running demo server:
//   1. desktop drag on the build board
//   2. the phone journey (tabs, add, draft)
// Run: node scripts/record-videos.mjs <baseURL> <outDir>
import { chromium } from "@playwright/test";

const baseURL = process.argv[2] ?? "http://localhost:8142";
const outDir = process.argv[3] ?? "recordings";

async function prepare(page) {
  await page.goto("/app/");
  await page.locator('a[href*="/pub/"]').first().click();
  await page.getByRole("button", { name: "Prepare next issue" }).click();
  await page.waitForURL(/\/app\/issues\/\d+/, { timeout: 120000 });
  await page
    .locator('section[aria-label="Pautas"] p.font-doc, [aria-label="Pautas"] p.font-doc')
    .first()
    .waitFor({ timeout: 120000 });
}

// html5 drag: react's onDragStart arms the ref, drop fires the move
async function dragCard(page, card, target) {
  const box = await card.boundingBox();
  const tbox = await target.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(tbox.x + tbox.width / 2, tbox.y + 10, { steps: 12 });
  const dt = await page.evaluateHandle(() => new DataTransfer());
  await card.locator("[draggable]").first().dispatchEvent("dragstart", { dataTransfer: dt });
  await target.dispatchEvent("dragover", { dataTransfer: dt });
  await page.waitForTimeout(300);
  await target.dispatchEvent("drop", { dataTransfer: dt });
  await page.mouse.up();
}

// ---------- video 1: desktop drag ----------
{
  const browser = await chromium.launch();
  const ctx = await browser.newContext({
    baseURL,
    viewport: { width: 1360, height: 800 },
    recordVideo: { dir: outDir + "/desktop", size: { width: 1360, height: 800 } },
  });
  const page = await ctx.newPage();
  await prepare(page);
  await page.waitForTimeout(1500);

  // drag a bench card into Quick links
  const benchCard = page.locator('section[aria-label="Pautas"] .card-in').nth(1);
  const target = page.locator('[data-section="Quick links"]');
  await dragCard(page, benchCard, target);
  await page.waitForTimeout(1200);

  // drag an issue card back to the bench
  const issueCard = page.locator('[data-section="Worth knowing"] .card-in').first();
  const bench = page.locator('section[aria-label="Pautas"]');
  await dragCard(page, issueCard, bench);
  await page.waitForTimeout(1200);

  await ctx.close();
  await browser.close();
  console.log("desktop video done");
}

// ---------- video 2: phone journey ----------
{
  const browser = await chromium.launch();
  const ctx = await browser.newContext({
    baseURL,
    viewport: { width: 375, height: 720 },
    recordVideo: { dir: outDir + "/phone", size: { width: 375, height: 720 } },
  });
  const page = await ctx.newPage();
  await prepare(page);
  await page.waitForTimeout(1200);

  // bench tab → add a story → issue tab → draft
  await page.getByRole("button", { name: /This issue/ }).click();
  await page.waitForTimeout(800);
  await page.getByRole("button", { name: /^Pautas/ }).click();
  await page.waitForTimeout(500);
  await page
    .locator('section[aria-label="Pautas"]')
    .getByRole("button", { name: "Add to issue", exact: true })
    .nth(1)
    .click();
  await page.waitForTimeout(500);
  const chooser = page.getByRole("dialog");
  if (await chooser.isVisible()) {
    await chooser.getByRole("button", { name: /Quick links/ }).click();
  }
  await page.waitForTimeout(1200);
  await page.getByRole("button", { name: /This issue/ }).click();
  await page.waitForTimeout(800);
  await page.getByRole("button", { name: /Draft the issue/ }).click();
  // let the draft rail tick
  await page.waitForTimeout(8000);

  await ctx.close();
  await browser.close();
  console.log("phone video done");
}
