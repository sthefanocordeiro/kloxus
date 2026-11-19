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

test("one button pattern: Avançar alone on page 1, Voltar + Avançar on page 2", async ({ page }) => {
  await onboard(page);

  // page 1 (Sua newsletter): the only in-flow action is Avançar —
  // it sits right-aligned, where the forward action lives
  const next = page.getByRole("button", { name: "Avançar", exact: true });
  await expect(next).toBeVisible();
  await expect(page.getByRole("button", { name: "Voltar", exact: true })).toHaveCount(0);
  const field = page.getByLabel("Linha editorial");
  const [nextBox, fieldBox] = await Promise.all([next.boundingBox(), field.boundingBox()]);
  expect(Math.abs(nextBox!.x + nextBox!.width - (fieldBox!.x + fieldBox!.width))).toBeLessThan(12);

  // page 2 (Seções): Voltar left, Avançar right
  await next.click();
  await expect(page.getByRole("heading", { name: "Suas seções" })).toBeVisible();
  const fwd = page.getByRole("button", { name: "Avançar", exact: true });
  await expect(fwd).toBeVisible();
  const back = page.getByRole("button", { name: "Voltar", exact: true });
  await expect(back).toBeVisible();
  const [backBox, fwdBox] = await Promise.all([back.boundingBox(), fwd.boundingBox()]);
  expect(backBox!.x).toBeLessThan(fwdBox!.x);

  // Voltar is live — it returns to the first step
  await back.click();
  await expect(page.getByLabel("Linha editorial")).toBeVisible();
});

async function createViaAPI(page: import("@playwright/test").Page) {
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
  return ((await res.json()).publication as { id: number }).id;
}

test("page 3 (Fontes) carries the same Voltar + Avançar pair", async ({ page }) => {
  const id = await createViaAPI(page);

  await page.goto(`/publications/${id}/sources`);
  const fwd = page.getByRole("button", { name: "Avançar", exact: true });
  await expect(fwd).toBeVisible({ timeout: 15000 });
  const back = page.getByRole("button", { name: "Voltar", exact: true });
  await expect(back).toBeVisible();
  // Voltar left, Avançar right — the same order as the other steps
  const [backBox, fwdBox] = await Promise.all([back.boundingBox(), fwd.boundingBox()]);
  expect(backBox!.x).toBeLessThan(fwdBox!.x);
  await back.click();
  await page.waitForURL(new RegExp(`/publications/${id}/sections`));
});

test("an account with a newsletter never reaches the create flow", async ({ page }) => {
  // create the publication via API first — the UI path must then refuse
  // entry to /new and land on the publication's editions instead
  const id = await createViaAPI(page);
  expect(id).toBeGreaterThan(0);

  await page.goto("/new");
  await page.waitForURL(/\/publications\/\d+/, { timeout: 15000 });
  await expect(page.getByLabel("Cole um link ou uma edição")).toHaveCount(0);
});
