import { expect, test } from "@playwright/test";
import { benchCol, boardReady, issueCol, onboard } from "./onboard.ts";
import { preflight } from "./mail.ts";
import { PUBLISHING_SKIP_REASON, publishingOff } from "./publishing.ts";

test.skip(publishingOff, PUBLISHING_SKIP_REASON);

// KLOX-89 — the KLOX-84 audit's only P0s, both on the phone layout:
//
// 1. The Pautas board's icon buttons ("Adicionar à edição", "Remover",
//    "Mover para…") rendered at ~28px — under the owner's ≥44px touch
//    rule (≥40px desktop). They now use the design-system icon size.
// 2. The issue's pane tab bar (Pautas / Edição / Fontes e fatos /
//    Prévia) rendered `position: static` at the very end of the
//    document flow — only reachable after scrolling the whole
//    edition, stacked under the global bottom nav. It is now fixed
//    just above the global nav.

test.beforeEach(async ({ page }) => {
  await preflight(page);
});

test("mobile 390px: pautas board controls meet the 44px hit-area rule", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await onboard(page);
  await boardReady(page, true);

  // the bench/issue column tabs are themselves touch targets
  const tabIssue = page.getByRole("button", { name: /This piece/ });
  const tabBench = page.getByRole("button", { name: /^Suggestions/ });
  for (const tab of [tabIssue, tabBench]) {
    const box = await tab.boundingBox();
    expect(box!.height).toBeGreaterThanOrEqual(44);
  }

  // issue column: each card's icon controls must be ≥44×44 — the
  // column sits behind its tab on phone
  await tabIssue.click();
  for (const name of ["Remove", /Move to/] as const) {
    const box = await issueCol(page)
      .getByRole("button", { name, exact: typeof name === "string" })
      .first()
      .boundingBox();
    expect(box!.height, String(name)).toBeGreaterThanOrEqual(44);
    expect(box!.width, String(name)).toBeGreaterThanOrEqual(44);
  }

  // bench column: the "+" add affordance — the audit's 31×28px offender
  await tabBench.click();
  const add = benchCol(page).getByRole("button", { name: "Add to piece", exact: true }).first();
  const addBox = await add.boundingBox();
  expect(addBox!.height).toBeGreaterThanOrEqual(44);
  expect(addBox!.width).toBeGreaterThanOrEqual(44);

  // the same controls stay ≥40px on desktop
  await page.setViewportSize({ width: 1440, height: 900 });
  const deskBox = await add.boundingBox();
  expect(deskBox!.height).toBeGreaterThanOrEqual(40);
  expect(deskBox!.width).toBeGreaterThanOrEqual(40);
});

test("mobile 390px: issue pane tab bar is pinned above the global nav", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await onboard(page);
  await boardReady(page, true);

  // manual compose lands the editor (and its tab bar) without a model
  // run — the deterministic mode's "Write it myself" path
  await page.getByRole("button", { name: /This piece/ }).click();
  await page.getByRole("button", { name: "Write it myself" }).click();
  await expect(page.locator(".ProseMirror").first()).toBeVisible({ timeout: 60_000 });

  const tabbar = page.getByRole("navigation", { name: "Piece panes" });
  const globalNav = page.getByRole("navigation", { name: "Radar" });
  await expect(tabbar).toBeVisible();

  // pinned inside the viewport and flush above the global nav — never
  // under it, never at the document's end
  const box = (await tabbar.boundingBox())!;
  const nav = (await globalNav.boundingBox())!;
  expect(box.y).toBeGreaterThan(0);
  expect(box.y + box.height).toBeLessThanOrEqual(nav.y + 1);
  expect(box.y + box.height).toBeGreaterThan(nav.y - 4);

  // every pane stays one tap away mid-document — scroll first, then
  // switch without ever reaching the document's end
  await page.evaluate(() => {
    document.querySelector("main")!.scrollTop = 300;
  });
  await tabbar.getByRole("button", { name: "Preview" }).click();
  await expect(page.getByText("Reader", { exact: true })).toBeVisible();
  await tabbar.getByRole("button", { name: "Stories" }).click();
  await expect(page.getByRole("button", { name: /— (approved|to decide)$/ }).first()).toBeVisible();
  await tabbar.getByRole("button", { name: "Piece" }).click();
  await expect(page.locator(".ProseMirror").first()).toBeVisible();

  // desktop keeps the old flow — the phone bar never renders at ≥768px
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect(tabbar).toBeHidden();
});
