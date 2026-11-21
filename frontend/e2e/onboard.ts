import { expect, type Page } from "@playwright/test";

// Shared onboarding helpers — every test walks its own anonymous
// workspace (fresh browser context) through landing → /new → sources
// → the first collection run, then waits for the board to fill.
// Extracted from journey.spec.ts so focused specs reuse the same path.

export const issueCol = (page: Page) => page.locator('section[aria-label="This piece"]');
export const benchCol = (page: Page) => page.locator('section[aria-label="Suggestions"]');

// The workspace starts empty — every journey walks onboarding first:
// landing → /new → /pub/:id/sources → confirm → first run → board.
export async function onboard(page: Page) {
  await page.goto("/");
  // the landing renders the hero headline before any workspace exists
  await expect(page.getByRole("heading", { name: /The research is done/ })).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: /Create your Radar/ }).first().click();
  await page.waitForURL(/\/new/);

  // the sample proposal pre-fills the five editable blocks
  await expect(page.locator('input[value*="Futuro"]')).toBeVisible({ timeout: 15000 });
  // step 2 is the section builder (KLOX-65): keep the classic template
  // the proposal suggested — the edition totals travel with it
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await expect(page.getByText(/Your piece will have \d+ stor/)).toBeVisible();
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.waitForURL(/\/publications\/\d+\/sources/);

  // the sources screen offers the fixture sources — preview one, then
  // confirm all: the first collection runs automatically into the board
  await expect(page.getByText("Suggested sources")).toBeVisible();
  await page.getByRole("button", { name: "Latest items", exact: true }).first().click();
  await expect(page.locator("ul li a").first()).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "Next", exact: true }).click();
  // the first real-mode run collects, dedupes and triages live feeds —
  // ~10-13 min hosted, seconds against fixtures
  await page.waitForURL(/\/issues\/\d+/, { timeout: process.env.E2E_BASE_URL ? 840_000 : 600_000 });
}

export async function boardReady(page: Page, isPhone: boolean) {
  // the step rail finishes, then the board lands. On phone the issue
  // column sits behind its tab — the tab bar is the ready signal there.
  await expect(
    isPhone
      ? page.getByRole("button", { name: /This issue/ })
      : page.getByRole("heading", { name: "This piece" }),
    // hosted runs the real pipeline (~10 min); local fixtures land fast
  ).toBeVisible({ timeout: process.env.E2E_BASE_URL ? 840_000 : 120_000 });
  // hosted: the bench fills when the real run ends (~10-13 min)
  await expect(benchCol(page).locator("p.font-doc").first()).toBeVisible({
    timeout: process.env.E2E_BASE_URL ? 840_000 : 60_000,
  });
}
