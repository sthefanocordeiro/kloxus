import { expect, test, type Page } from "@playwright/test";

// KLOX-90 (KLOX-84 audit P1): the first-run path a judge walks —
// publication created, sources not yet confirmed, no edition yet.
// The pub home must gate "Preparar próxima edição" instead of letting
// the run die on "no items collected", the Edições list must carry
// real empty copy, and the sidebar must not glow "Edição atual" on
// pages where no edition exists.

async function createdNoSources(page: Page) {
  await page.goto("/");
  await page.getByRole("button", { name: /minha newsletter/ }).first().click();
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
    const prepare = page.getByRole("button", { name: "Preparar próxima edição" });
    await expect(prepare).toBeDisabled();
    await expect(page.getByText(/Confirme suas fontes primeiro/)).toBeVisible();
    await expect(page.getByRole("link", { name: "Abrir fontes" })).toHaveAttribute(
      "href",
      `/publications/${pubID}/sources`,
    );

    // the Edições list carries copy, never a bare dash
    await expect(page.getByText("Nenhuma edição ainda — prepare a primeira.")).toBeVisible();
  });

  test("nav: only the route's section glows", async ({ page }) => {
    const pubID = await createdNoSources(page);

    // pub home — "Edições" glows; "Edição atual" points at the same URL
    // but must not glow (no edition exists)
    await page.goto(`/publications/${pubID}`);
    await expect(navLink(page, "Edição atual")).not.toHaveClass(/bg-teal-soft/);
    await expect(navLink(page, "Edições")).toHaveClass(/bg-teal-soft/);

    // sources sub-page — only "Fontes" glows
    await page.goto(`/publications/${pubID}/sources`);
    await expect(navLink(page, "Edição atual")).not.toHaveClass(/bg-teal-soft/);
    await expect(navLink(page, "Edições")).not.toHaveClass(/bg-teal-soft/);
    await expect(navLink(page, "Fontes")).toHaveClass(/bg-teal-soft/);
  });
});
