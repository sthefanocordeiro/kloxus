import { expect, test } from "@playwright/test";

// KLOX-127: the landing hero captures a waitlist email. Uses a
// reserved example.com address so hosted runs add no real contact.
test("landing waitlist: invalid, then success; app entry stays", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "PT" }).click();
  await expect(page.getByRole("heading", { name: /A pauta pronta/ })).toBeVisible({ timeout: 15000 });

  const input = page.getByLabel("Seu melhor e-mail");
  await input.fill("nao-e-email");
  await page.getByRole("button", { name: "Avise-me quando lançar" }).click();
  await expect(page.getByRole("alert")).toContainText(/e-mail válido/);

  await input.fill(`e2e-waitlist@example.com`);
  await page.getByRole("button", { name: "Avise-me quando lançar" }).click();
  await expect(page.getByText("Você está na lista.")).toBeVisible();
  await expect(page.getByRole("button", { name: /Já sou criador/ })).toBeVisible();
});
