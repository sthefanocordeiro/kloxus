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
    await expect(page.getByRole("heading", { name: "Today's stories" })).toBeVisible({ timeout: 30_000 });
    const boxes = page.getByRole("checkbox");
    await expect(boxes.first()).toBeVisible({ timeout: 30_000 });
    await boxes.nth(1).check();
    await boxes.nth(0).check();
    await expect(page.getByText("2 stories selected")).toBeVisible();
    await page.getByRole("button", { name: "Build brief →" }).click();

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

    // KLOX-215: walk the last step too — tick a destination, copy its
    // version, and the API must record the piece exported with that
    // format. Copy needs the clipboard grant or the write is refused
    // and the piece is never marked.
    const issueID = page.url().match(/\/pauta\/(\d+)\/export/)![1];
    await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.getByRole("checkbox", { name: "LinkedIn post" }).check();
    const copy = page.getByRole("button", { name: "Copy", exact: true });
    await expect(copy).toBeVisible({ timeout: 15_000 });
    await copy.click();
    await expect(page.getByRole("button", { name: "Copied" })).toBeVisible();

    // markExported is fire-and-forget after the copy — poll the API
    // until the export lands instead of asserting on a race
    await expect(async () => {
      const res = await page.request.get(`/api/v1/issues/${issueID}`);
      expect(res.ok()).toBeTruthy();
      const { issue } = (await res.json()) as {
        issue: { exported?: boolean; exported_formats?: string[] };
      };
      expect(issue.exported).toBe(true);
      expect(issue.exported_formats).toContain("linkedin");
    }).toPass({ timeout: 15_000 });
  });
});
