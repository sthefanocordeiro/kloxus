import { expect, type Page } from "@playwright/test";
import { claimOwner } from "./mail.ts";

// Shared onboarding helper — every test walks its own anonymous
// workspace (fresh browser context) through landing → /new → sources
// → the first collection run, then waits for the pauta editor.
// Extracted from journey.spec.ts so focused specs reuse the same path.

// The workspace starts empty — every journey walks onboarding first:
// landing → /new → /pub/:id/sources → confirm → first run → /pauta/:id.
export async function onboard(page: Page) {
  await page.goto("/");
  // the landing renders the hero headline before any workspace exists
  await expect(page.getByRole("heading", { name: /The research is ready/ })).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: /Create your Radar/ }).first().click();
  await page.waitForURL(/\/start/);
  await claimOwner(page);
  await page.goto("/new"); // legacy create flow: still covered here, no longer the front door

  // the sample proposal pre-fills the five editable blocks
  await expect(page.locator('input[value*="Futuro"]')).toBeVisible({ timeout: 15000 });
  // step 2 is the section builder (KLOX-65): keep the classic template
  // the proposal suggested — the edition totals travel with it
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await expect(page.getByText(/Your brief will have \d+ stor/)).toBeVisible();
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.waitForURL(/\/publications\/\d+\/sources/);

  // the sources screen offers the fixture sources — preview one, then
  // confirm all: the first collection runs automatically into the pauta
  await expect(page.getByText("Suggested sources")).toBeVisible();
  await page.getByRole("button", { name: "Latest items", exact: true }).first().click();
  await expect(page.locator("ul li a").first()).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "Next", exact: true }).click();
  // the first real-mode run collects, dedupes and triages live feeds —
  // ~10-13 min hosted, seconds against fixtures; it lands on /pauta/:id
  await page.waitForURL(/\/pauta\/\d+/, { timeout: process.env.E2E_BASE_URL ? 840_000 : 600_000 });
}
