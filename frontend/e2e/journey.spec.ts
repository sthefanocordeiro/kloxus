import { expect, test, type Page } from "@playwright/test";

// The main path at both widths: prepare → decisions → draft → fix the
// flag → type commentary (Saved, survives reload) → approve.

async function decideAll(page: Page) {
  // approve every pending story — the Approve button disappears once a
  // card is decided, so the loop terminates at zero
  const approve = page.getByRole("button", { name: "Approve", exact: true });
  await expect(approve.first()).toBeVisible({ timeout: 30000 });
  for (let i = 0; i < 30 && (await approve.count()) > 0; i++) {
    await approve.first().click();
    await page.waitForTimeout(400);
  }
  await expect(page.getByText(/0 to decide/)).toBeVisible();
}

async function clearFlags(page: Page) {
  // flagged sentences offer Edit / Mark as interpretation / Remove —
  // Remove is deterministic for the demo fixture
  const remove = page.getByRole("button", { name: "Remove" });
  for (let i = 0; i < 20; i++) {
    if ((await remove.count()) === 0) break;
    await remove.first().click();
    await page.waitForTimeout(800);
  }
}

async function typeCommentary(page: Page) {
  // every required commentary block gets text; autosave must say Saved
  const blocks = page.locator("[data-commentary]");
  await expect(blocks.first()).toBeVisible({ timeout: 30000 });
  const n = await blocks.count();
  for (let i = 0; i < n; i++) {
    const editable = blocks.nth(i).locator("[data-node-view-content]");
    await editable.click();
    // ProseMirror folds the DOM caret into state.selection asynchronously
    // (selectionchange) — typing ahead of it hits the stale selection.
    // Wait until the caret is a text cursor inside this commentary node.
    await page.waitForFunction(() => {
      const s = (window as any).__ed?.state.selection;
      return !!s && s.empty && s.$from?.node(-1)?.type?.name === "commentary";
    }, null, { timeout: 15000 });
    await page.keyboard.type("My reading of this section, for the record.", { delay: 15 });
  }
  await expect(page.getByText("Saved")).toBeVisible({ timeout: 15000 });
}

async function approveIssue(page: Page) {
  const btns = page.getByRole("button", { name: "Approve issue" });
  // header trigger opens the readiness popover; its Approve button is
  // rendered only when every check is green — waiting for it also waits
  // out any readiness refetch race
  await btns.first().click();
  await expect(btns.nth(1)).toBeVisible({ timeout: 30000 });
  await btns.nth(1).click();
  // the popover closes when the confirm dialog grabs focus — identify
  // the dialog by its unique Cancel button (popover content is also
  // role=dialog in Radix)
  const dialog = page
    .getByRole("dialog")
    .filter({ has: page.getByRole("button", { name: "Cancel" }) });
  await dialog.getByRole("button", { name: "Approve issue" }).click();
  await expect(page.getByText("approved", { exact: false }).first()).toBeVisible({ timeout: 15000 });
}

const viewports = [
  { name: "desktop", width: 1360, height: 850 },
  { name: "phone", width: 375, height: 740 },
];

for (const vp of viewports) {
  test(`demo journey at ${vp.name} (${vp.width}px)`, async ({ page }) => {
    await page.setViewportSize({ width: vp.width, height: vp.height });
    await page.goto("/app/");
    await page.locator('a[href*="/pub/"]').first().click();

    // prepare → lands on the issue's decisions view
    await page.getByRole("button", { name: "Prepare next issue" }).click();
    await page.waitForURL(/\/app\/issues\/\d+/, { timeout: 120000 });

    await decideAll(page);
    await page.getByRole("button", { name: "Draft the issue" }).click();

    // editor: sentence nodes render once the draft lands
    await expect(page.locator("[data-sid]").first()).toBeVisible({ timeout: 120000 });

    await clearFlags(page);
    await typeCommentary(page);

    // reload — commentary must persist
    const typed = "My reading of this section, for the record.";
    await page.reload();
    await expect(page.locator("[data-commentary]").getByText(typed).first()).toBeVisible({
      timeout: 30000,
    });

    await approveIssue(page);
  });
}

test("demo reset restores the fixture after items run out", async ({ page }) => {
  await page.setViewportSize({ width: 1360, height: 850 });
  await page.goto("/app/");
  await page.locator('a[href*="/pub/"]').first().click();
  await page.getByRole("button", { name: "Prepare next issue" }).click();
  await page.waitForURL(/\/app\/issues\/\d+/, { timeout: 120000 });

  // fixture exhausted: a second prepare must say so, not fail silently
  await page.goBack();
  await page.getByRole("button", { name: "Prepare next issue" }).click();
  await expect(page.getByText(/Nothing new/)).toBeVisible({ timeout: 120000 });

  // reset restores the fixture — prepare works again
  await page.getByRole("button", { name: "Reset demo" }).click();
  await expect(page.getByRole("button", { name: "Prepare next issue" })).toBeEnabled({
    timeout: 30000,
  });
  await page.getByRole("button", { name: "Prepare next issue" }).click();
  await page.waitForURL(/\/app\/issues\/\d+/, { timeout: 120000 });
});
