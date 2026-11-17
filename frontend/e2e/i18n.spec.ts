import { expect, test } from "@playwright/test";

// KLOX-56: the interface chrome follows the browser language — a pt-BR
// visitor gets <html lang="pt-BR"> and pt chrome with no English leak;
// the default context stays en (the other specs cover it).
test.describe("interface language", () => {
  test.use({ locale: "pt-BR" });

  test("pt-BR landing: html lang and pt chrome", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator("html")).toHaveAttribute("lang", "pt-BR");
    await expect(
      page.getByRole("button", { name: /Começar sem login/ }).first(),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: /Get started without login/ }),
    ).toHaveCount(0);
  });
});
