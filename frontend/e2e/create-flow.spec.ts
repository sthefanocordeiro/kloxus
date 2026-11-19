import { expect, test } from "@playwright/test";

// KLOX-79: the create-newsletter flow guards its entry — an account
// that already has a newsletter never sees the form (redirected to its
// editions), the paste-to-create option stays disabled behind an
// in-development notice, the sample's editorial line carries no
// fictional place names, and Voltar is a working control.
test.use({ locale: "pt-BR" });

async function onboard(page: import("@playwright/test").Page) {
  await page.goto("/");
  await page
    .getByRole("button", { name: /minha newsletter/ })
    .first()
    .click();
  await page.waitForURL(/\/new/);
  await expect(page.getByLabel("Linha editorial")).toBeVisible({ timeout: 15000 });
}

test("create flow: disabled paste option, realistic example, working Voltar", async ({ page }) => {
  await onboard(page);

  // the paste/link option is not launched — disabled control + notice
  await expect(page.getByLabel("Cole um link ou uma edição")).toBeDisabled();
  await expect(page.getByRole("button", { name: "Criar a partir disso" })).toBeDisabled();
  await expect(
    page.getByText("Esta função ainda não foi lançada — está em desenvolvimento."),
  ).toBeVisible();

  // the sample's fields read realistic — no fictional place names
  await expect(page.getByLabel("Linha editorial")).not.toHaveValue(/Valdória/i);
  await expect(page.getByLabel("Tema")).not.toHaveValue(/valdoriana/i);

  // Voltar leaves /new for the publications list — no dead controls
  await page.getByRole("link", { name: "Voltar para Publicações" }).click();
  await page.waitForURL(/\/publications$/);
});

test("an account with a newsletter never reaches the create flow", async ({ page }) => {
  // create the publication via API first — the UI path must then refuse
  // entry to /new and land on the publication's editions instead
  const proposal = (await (await page.request.get("/api/v1/proposal")).json()).proposal;
  const res = await page.request.post("/api/v1/publications", {
    data: {
      name: proposal.name,
      topic: proposal.topic,
      audience: proposal.audience,
      editorial_line: proposal.editorial_line,
      tone: proposal.tone,
      language: proposal.language,
      frequency: proposal.frequency,
      ai_disclosure: proposal.ai_disclosure,
      sections: proposal.sections,
      paywall: proposal.paywall,
    },
  });
  expect(res.status()).toBe(201);

  await page.goto("/new");
  await page.waitForURL(/\/publications\/\d+/, { timeout: 15000 });
  await expect(page.getByLabel("Cole um link ou uma edição")).toHaveCount(0);
});
