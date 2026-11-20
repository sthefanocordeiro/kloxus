import { expect, test } from "@playwright/test";

// KLOX-79: the create-newsletter flow guards its entry — an account
// that already has a newsletter never sees the form (redirected to its
// editions), the sample's editorial line carries no fictional place
// names, and Voltar is a working control. KLOX-99: the "Cole um link"
// option is live — a pasted URL goes to the scraper and the extracted
// proposal fills the editable form for review before anything is
// created (demo runs the simulated provider and says so).
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

test("create flow: live paste option, realistic example, working Voltar", async ({ page }) => {
  await onboard(page);

  // the paste/link option is live — enabled control, no dead notice
  const paste = page.getByLabel("Cole um link");
  await expect(paste).toBeEnabled();
  await expect(page.getByRole("button", { name: "Criar a partir disso" })).toBeDisabled(); // empty input
  await expect(page.getByText("está em desenvolvimento")).toHaveCount(0);

  // pasting a URL proposes a template for review — the demo's provider
  // is simulated and the preview says so honestly
  await paste.fill("https://meu-blog.com.br");
  await page.getByRole("button", { name: "Criar a partir disso" }).click();
  const preview = page.getByTestId("scrape-preview");
  await expect(preview).toBeVisible();
  await expect(preview.getByText("Demonstração — nenhuma página foi buscada")).toBeVisible();
  await expect(preview).toContainText("Do seu link");
  // the extracted proposal replaced the sample in the editable form —
  // the fake provider derives the site name from the pasted host
  await expect(page.getByLabel("Nome")).toHaveValue("Meu Blog");
  await expect(page.getByLabel("Linha editorial")).toHaveValue(/Priorize/);
  // nothing was created — the flow still waits at step 1
  await expect(page.getByRole("button", { name: "Avançar", exact: true })).toBeVisible();
});

test("create flow: sample fields stay realistic and Voltar works", async ({ page }) => {
  await onboard(page);

  // the sample's fields read realistic — no fictional place names.
  // exact: the KLOX-94 reject-terms field ("Nunca pautar — Temas
  // separados") also matches a plain /Tema/ lookup
  await expect(page.getByLabel("Linha editorial")).not.toHaveValue(/Valdória/i);
  await expect(page.getByLabel("Tema", { exact: true })).not.toHaveValue(/valdoriana/i);

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

// KLOX-79 owner rework: at ≤390px nothing in the create flow may clip
// horizontally — bare `grid` containers used to size an implicit auto
// track to the widest child's max-content (~470px at phone widths),
// pushing chips and the delete affordance past the right edge. Explicit
// grid-cols-1 bounds every track to the viewport (same fix as KLOX-86).
test("create flow has no horizontal clipping at 390px", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await onboard(page);

  const fitsViewport = async () => {
    const { scrollWidth, clientWidth } = await page.evaluate(() => ({
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
    }));
    expect(scrollWidth).toBeLessThanOrEqual(clientWidth);
  };
  const insideViewport = async (name: string | RegExp) => {
    const box = await page.getByRole("button", { name, exact: true }).first().boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  };

  // step 1 — Sua newsletter
  await fitsViewport();

  // step 2 — Seções: template chips wrap inside the viewport and every
  // section row's delete affordance stays reachable
  await page.getByRole("button", { name: "Avançar", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Suas seções" })).toBeVisible();
  await fitsViewport();
  for (const chip of ["Clássica", "Resumo curto", "Ensaio + links", "Em branco"]) {
    await insideViewport(chip);
  }
  const removes = page.getByRole("button", { name: "Remover seção", exact: true });
  const rows = await removes.count();
  expect(rows).toBeGreaterThan(0);
  for (let i = 0; i < rows; i++) {
    const box = await removes.nth(i).boundingBox();
    expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  }

  // step 3 — Fontes: the source cards fit the viewport as well
  await page.getByRole("button", { name: "Avançar", exact: true }).click();
  await page.waitForURL(/\/publications\/\d+\/sources/, { timeout: 15000 });
  await expect(page.getByRole("button", { name: "Avançar", exact: true })).toBeVisible({
    timeout: 15000,
  });
  await fitsViewport();
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
  await expect(page.getByLabel("Cole um link")).toHaveCount(0);
});
