import { expect, test, type Locator } from "@playwright/test";
import { onboard } from "./onboard.ts";

// KLOX-180 — the 24/11 owner's e2e P0 on the phone layout: the fixed
// action bars ("Build brief →" on Hoje, "Go to export" on Escrever)
// rendered at bottom-0 under the z-40 global nav — the two bars stacked
// at ~755px and a tap on the button opened Settings instead. Both bars
// are now pinned above the nav; a Playwright click doubles as the
// tap-proof — a covered button would intercept on the nav and time out.

const expectAboveNav = async (bar: Locator, nav: Locator) => {
  const box = (await bar.boundingBox())!;
  const nv = (await nav.boundingBox())!;
  expect(box.y).toBeGreaterThan(0);
  // flush above the global nav — never under it
  expect(box.y + box.height).toBeLessThanOrEqual(nv.y + 1);
};

test("mobile 375px: Build brief and Go to export sit above the bottom nav", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await onboard(page);
  const pubs = await (await page.request.get("/api/v1/publications")).json();
  const pubID = pubs.publications[0].id;

  const globalNav = page.getByRole("navigation", { name: "Radar" });

  // Hoje — ticking a card raises the action bar
  await page.goto(`/publications/${pubID}/today`);
  const boxes = page.getByRole("checkbox");
  await expect(boxes.first()).toBeVisible({ timeout: 30_000 });
  await boxes.first().check();

  const buildBar = page.getByRole("region", { name: "Build brief →" });
  await expect(buildBar).toBeVisible();
  await expectAboveNav(buildBar, globalNav);
  await buildBar.getByRole("button", { name: "Build brief →" }).click();

  // Escrever — the export bar is always on this page
  await page.waitForURL(/\/pauta\/\d+$/);
  const exportBar = page.getByRole("region", { name: "Go to export" });
  await expect(exportBar).toBeVisible();
  await expectAboveNav(exportBar, globalNav);
  await page.getByRole("button", { name: "Go to export" }).click();

  await page.waitForURL(/\/pauta\/\d+\/export/);
  await expect(page.getByRole("heading", { name: "Export" })).toBeVisible();
});
