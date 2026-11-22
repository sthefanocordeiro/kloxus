import { expect, test, type Page } from "@playwright/test";
import { publishingOff } from "./publishing.ts";

// KLOX-122: the sidebar differs by flag — Hoje/Radar when publishing is off.
const CURRENT = publishingOff ? "Hoje" : "Peça atual";
const SOURCES = publishingOff ? "Radar" : "Fontes";

// KLOX-90 (KLOX-84 audit P1): the first-run path a judge walks —
// publication created, sources not yet confirmed, no edition yet.
// The pub home must gate "Preparar próxima peça" instead of letting
// the run die on "no items collected", the Edições list must carry
// real empty copy, and the sidebar must not glow "Peça atual" on
// pages where no edition exists.

async function createdNoSources(page: Page) {
  await page.goto("/");
  await page.getByRole("button", { name: /meu Radar/ }).first().click();
  await page.waitForURL(/\/new/);
  await page.getByRole("button", { name: "Avançar", exact: true }).click();
  await page.getByRole("button", { name: "Avançar", exact: true }).click();
  // onboarding lands on the sources screen — nothing confirmed yet
  await page.waitForURL(/\/publications\/(\d+)\/sources/);
  return page.url().match(/\/publications\/(\d+)\/sources/)![1];
}

// the desktop sidebar nav: exactly one item glows — the active section
const navLink = (page: Page, name: string) =>
  page.locator("aside nav a", { hasText: name }).first();

test.describe("first-run path (KLOX-90)", () => {
  test.use({ locale: "pt-BR" });

  test("pub home: Prepare gated, empty Edições copy, sources link", async ({ page }) => {
    const pubID = await createdNoSources(page);
    await page.goto(`/publications/${pubID}`);

    // gated: the button stays disabled and the hint points at Fontes —
    // the dead-end "no items collected" run can no longer start
    const prepare = page.getByRole("button", { name: "Preparar próxima peça" });
    await expect(prepare).toBeDisabled();
    await expect(page.getByText(/Confirme suas fontes primeiro/)).toBeVisible();
    await expect(page.getByRole("link", { name: "Abrir fontes" })).toHaveAttribute(
      "href",
      `/publications/${pubID}/sources`,
    );

    // the Edições list carries copy, never a bare dash
    await expect(page.getByText("Nenhuma peça ainda — prepare a primeira.")).toBeVisible();
  });

  test("nav: only the route's section glows", async ({ page }) => {
    const pubID = await createdNoSources(page);

    // pub home — "Peças" glows; "Peça atual" points at the same URL
    // but must not glow (no edition exists)
    await page.goto(`/publications/${pubID}`);
    await expect(navLink(page, CURRENT)).not.toHaveClass(/bg-teal-soft/);
    await expect(navLink(page, "Peças")).toHaveClass(/bg-teal-soft/);

    // sources sub-page — only "Fontes" glows
    await page.goto(`/publications/${pubID}/sources`);
    await expect(navLink(page, CURRENT)).not.toHaveClass(/bg-teal-soft/);
    await expect(navLink(page, "Peças")).not.toHaveClass(/bg-teal-soft/);
    await expect(navLink(page, SOURCES)).toHaveClass(/bg-teal-soft/);
  });

  // KLOX-68: the board must survive a write-view round trip. The
  // pagination-reset effect used to wipe the restored board on every
  // mount; a refetch returning identical JSON kept the same data
  // reference (structural sharing), so nothing ever re-set it and the
  // board stayed a skeleton.
  test("board comes back after Escrever → Pautas", async ({ page }) => {
    test.setTimeout(240_000);
    await page.goto("/");
    await page.getByRole("button", { name: /meu Radar/ }).first().click();
    await page.waitForURL(/\/new/);
    await page.getByRole("button", { name: "Avançar", exact: true }).click();
    await page.getByRole("button", { name: "Avançar", exact: true }).click();
    await page.waitForURL(/\/publications\/(\d+)\/sources/);
    await page.getByRole("button", { name: "Avançar", exact: true }).click();
    await page.waitForURL(/\/issues\/\d+/, { timeout: 600_000 });

    const storyTitle = page.locator("p.font-doc").first();
    await expect(storyTitle).toBeVisible({ timeout: 120_000 });

    await page.getByRole("button", { name: /Escrever eu mesmo/ }).click();
    await expect(page.getByRole("button", { name: "Pautas", exact: true }).first()).toBeVisible();
    await page.getByRole("button", { name: "Pautas", exact: true }).first().click();
    await expect(storyTitle).toBeVisible({ timeout: 15_000 });
  });
});
