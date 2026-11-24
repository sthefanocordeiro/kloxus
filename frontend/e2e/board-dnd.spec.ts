import { expect, test, type Page } from "@playwright/test";
import { preflight, claimOwner } from "./mail.ts";
import { PUBLISHING_SKIP_REASON, publishingOff } from "./publishing.ts";

test.skip(publishingOff, PUBLISHING_SKIP_REASON);

// KLOX-97: the board explainer promises drag ("Drop a story here" /
// "Solte uma pauta aqui") but the owner's E2E found pautas do not drag
// between sections. These specs exercise the real HTML5 drag path —
// pointer down, move, drop — not the "+" / "Move to" buttons:
//
//   1. a bench card dragged by its grip handle seats in a section slot
//   2. a bench card dragged by its card body (the natural gesture — the
//      grip is a 14px low-contrast icon) seats the same way
//   3. a seated pauta dragged to another section moves
//
// Buttons and the keyboard keep working in the other suites; here the
// drag itself is the contract under test.

const issueCol = (page: Page) => page.locator('section[aria-label="This brief"]');
const benchCol = (page: Page) => page.locator('section[aria-label="Suggestions"]');
const sectionBox = (page: Page, name: string) =>
  issueCol(page).locator(`[data-section="${name}"]`);

// same onboarding walk as the demo journey: landing → /new → sources →
// the first run lands on the build board
async function onboard(page: Page) {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: /The research is ready/ })).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: /Create your Radar/ }).first().click();
  await page.waitForURL(/\/start/);
  await claimOwner(page);
  await page.goto("/new"); // legacy create flow: still covered here, no longer the front door
  await expect(page.locator('input[value*="Futuro"]')).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await expect(page.getByText(/Your brief will have \d+ stor/)).toBeVisible();
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.waitForURL(/\/publications\/\d+\/sources/);
  await expect(page.getByText("Suggested sources")).toBeVisible();
  await page.getByRole("button", { name: "Latest items", exact: true }).first().click();
  await expect(page.locator("ul li a").first()).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.waitForURL(/\/issues\/\d+/, { timeout: 600_000 });
}

async function boardReady(page: Page) {
  await expect(page.getByRole("heading", { name: "This brief" })).toBeVisible({ timeout: 120_000 });
  await expect(benchCol(page).locator("p.font-doc").first()).toBeVisible({ timeout: 60_000 });
}

// the deterministic run seats every slot — open one in "Vale saber" so
// a drop has room to land (a drop onto a full section opens the swap
// dialog, which is the buttons' contract, not the drag's). phaseA
// cards remove through the reason menu: "just remove" skips the reason.
async function openOneSeat(page: Page) {
  await sectionBox(page, "Vale saber").getByRole("button", { name: "Remove", exact: true }).first().click();
  await page.getByRole("button", { name: "just remove" }).click();
  await expect(page.getByText(/^Removed “/)).toBeVisible({ timeout: 15000 });
  await expect(sectionBox(page, "Vale saber").locator(".border-dashed").first()).toBeVisible({ timeout: 15000 });
}

// the drag contract: after the drop the toast confirms and the story
// is seated in the target section
async function expectSeated(page: Page, section: string, title: string) {
  await expect(page.getByText(/^(Added|Moved) “/)).toBeVisible({ timeout: 15000 });
  await expect(sectionBox(page, section).locator("p.font-doc", { hasText: title })).toBeVisible({ timeout: 15000 });
}

test.beforeEach(async ({ page }) => {
  await preflight(page);
});

test("bench card drags by its grip handle into a section slot", async ({ page }) => {
  await page.setViewportSize({ width: 1360, height: 850 });
  await onboard(page);
  await boardReady(page);
  await openOneSeat(page);

  const card = benchCol(page).locator(".card-in").first();
  const title = (await card.locator("p.font-doc").textContent())!.trim();
  const grip = card.locator('span[draggable="true"]');
  await expect(grip).toBeVisible();

  await grip.dragTo(sectionBox(page, "Vale saber").locator(".border-dashed").first());
  await expectSeated(page, "Vale saber", title);
});

test("bench card drags by its card body into a section slot", async ({ page }) => {
  await page.setViewportSize({ width: 1360, height: 850 });
  await onboard(page);
  await boardReady(page);
  await openOneSeat(page);

  const card = benchCol(page).locator(".card-in").first();
  const title = (await card.locator("p.font-doc").textContent())!.trim();

  // the natural gesture: grab the card itself, not the tiny grip
  await card.dragTo(sectionBox(page, "Vale saber").locator(".border-dashed").first());
  await expectSeated(page, "Vale saber", title);
});

test("seated pauta drags to another section", async ({ page }) => {
  await page.setViewportSize({ width: 1360, height: 850 });
  await onboard(page);
  await boardReady(page);
  await openOneSeat(page);

  // "O destaque" seats exactly one pauta (cap 1) — drag it into the
  // "Vale saber" seat just opened
  const seated = sectionBox(page, "O destaque").locator(".card-in").first();
  const title = (await seated.locator("p.font-doc").textContent())!.trim();

  await seated.locator('span[draggable="true"]').dragTo(
    sectionBox(page, "Vale saber").locator(".border-dashed").first(),
  );
  await expectSeated(page, "Vale saber", title);
});
