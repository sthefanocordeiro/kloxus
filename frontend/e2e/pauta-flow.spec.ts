import { expect, test } from "@playwright/test";
import { onboard } from "./onboard.ts";

// KLOX-168: the main flow — tick news on Hoje → Montar pauta → write
// free text → Ir para exportar → the export carries the author's text.
test.describe("main flow (KLOX-168)", () => {
  test("tick two news, write, export", async ({ page }) => {
    await onboard(page);
    const pubs = await (await page.request.get("/api/v1/publications")).json();
    const pubID = pubs.publications[0].id;

    await page.goto(`/publications/${pubID}/today`);
    await expect(page.getByRole("heading", { name: "Today's news" })).toBeVisible({ timeout: 30_000 });
    const boxes = page.getByRole("checkbox");
    await expect(boxes.first()).toBeVisible({ timeout: 30_000 });
    await boxes.nth(1).check();
    await boxes.nth(0).check();
    await expect(page.getByText("2 stories selected")).toBeVisible();
    await page.getByRole("button", { name: "Build pauta →" }).click();

    await page.waitForURL(/\/pauta\/\d+$/);
    await expect(page.getByTestId("pauta-entry")).toHaveCount(2);
    // KLOX-162: each news has a "Comment" button that opens its box
    const firstEntry = page.getByTestId("pauta-entry").first();
    await firstEntry.getByRole("button", { name: "Comment" }).click();
    const first = firstEntry.locator("textarea");
    await first.fill("Minha nota sobre a primeira notícia.");
    await expect(page.getByText("Saved")).toBeVisible({ timeout: 10_000 });

    // leave the second one empty: only a heads-up, never a blocker
    await page.getByRole("button", { name: "Go to export" }).click();
    await page.waitForURL(/\/pauta\/\d+\/export/);
    await expect(page.getByText(/1 story has no text/)).toBeVisible();
    await expect(page.getByRole("heading", { name: "Export" })).toBeVisible();
  });
});
