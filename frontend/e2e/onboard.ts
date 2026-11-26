import { expect, type Page } from "@playwright/test";
import { claimOwner, preflight } from "./mail.ts";

// Shared onboarding helper — every test walks its own anonymous
// workspace (fresh browser context) through landing → /start → claim →
// Criar Radar → skip Dar match → Notícias de hoje (KLOX-179: the legacy
// /new → sections → collection-run path is gone; Hoje builds the deck
// on demand).

export async function onboard(page: Page, ownerEmail?: string) {
  // transport preflight first: onboarding claims and creates a Radar —
  // a live or unknown mail transport must fail before any of that runs
  // (KLOX-215; the mutating specs used to rely on claimOwner's check
  // alone, which never verified the demo pipeline or outbox transport)
  await preflight(page);
  await page.goto("/");
  // the landing renders the hero headline before any workspace exists
  await expect(page.getByRole("heading", { name: /The research is ready/ })).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: /Create your Radar/ }).first().click();
  await page.waitForURL(/\/start/);
  // Account ownership is recovered across sessions (KLOX-272), so each
  // isolated test workspace needs its own owner email — reusing one
  // address would sign the next test back into the previous Radar.
  const email = ownerEmail ?? `owner-${Date.now()}-${Math.floor(Math.random() * 1e9)}@example.com`;
  await claimOwner(page, email);

  // the email gate polls until the claim lands (KLOX-161) — the about
  // form only renders once the workspace is claimed
  const name = page.getByLabel("Radar name");
  await expect(name).toBeVisible({ timeout: 20000 });
  await name.fill("Futuro Próximo");
  await page.getByLabel("Topics (comma separated)").fill("fintech, startups");
  await page.getByRole("button", { name: "Continue", exact: true }).click();

  // Dar match: a real deck shows the Skip button inside the card; an
  // empty deck shows a plain Continue instead — take whichever lands
  await expect(page.getByRole("heading", { name: "Match", exact: true })).toBeVisible();
  const skip = page.getByRole("button", { name: "Skip", exact: true })
    .or(page.getByRole("button", { name: "Continue", exact: true }));
  await skip.first().click({ timeout: 60_000 });

  // Pronto! → the link always opens Notícias de hoje (the primary
  // button stays disabled while the deck loads)
  await expect(page.getByRole("heading", { name: "Done!" })).toBeVisible();
  await page.getByRole("link", { name: "See today's stories" }).click();
  await page.waitForURL(/\/publications\/\d+\/today/);
  return email;
}
