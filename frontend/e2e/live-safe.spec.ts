import { expect, test } from "@playwright/test";
import {
  e2eMode,
  liveSafe,
  preflightLiveSafe,
  workspaceInfo,
} from "./mail.ts";

// KLOX-176 live-safe subset — READ-ONLY against a live-mail hosted
// target (https://kloxus.fly.dev runs mode=real mail=live, which the
// regular hosted suite refuses by design). Opt-in:
//
//   E2E_BASE_URL=https://kloxus.fly.dev E2E_LIVE_SAFE=1 npx playwright test
//
// Under that flag playwright.config collects only this file — the
// explicit read-only subset — and every test re-proves the target's
// own posture (assertLiveSafe) before touching it. No claims, no
// sends, no publications, no runs, no model calls: every check is a
// GET or a request the server must refuse. The mutating journeys stay
// under the assertTransport preflight and keep refusing a mail=live
// target closed — this file is the only code path the flag widens.
test.skip(
  !(e2eMode === "hosted" && liveSafe),
  "E2E_BASE_URL + E2E_LIVE_SAFE=1 to run",
);

test.beforeEach(async ({ page }) => {
  await preflightLiveSafe(page);
});

test("posture evidence: real app, live resend mail, unclaimed", async ({
  page,
}) => {
  const ws = await workspaceInfo(page);
  expect(ws).toMatchObject({
    hosted: true,
    mode: "real",
    mail: "live",
    mail_transport: "resend",
    claimed: false,
    mail_webhook: "configured",
  });
  // Read-only checks never reach the model — llm off|auto both qualify,
  // an unknown posture fails in assertLiveSafe before this test runs.
  expect(["off", "auto"]).toContain(ws.llm_mode);
});

test("landing serves the app shell", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("lang", /.+/);
  await expect(page.locator("#root")).toBeAttached();
  await expect(
    page.getByRole("button", { name: /Radar/ }).first(),
  ).toBeVisible();
});

test("sample API answers without a workspace", async ({ page }) => {
  const res = await page.request.get("/api/v1/sample");
  expect(res.ok()).toBeTruthy();
  const body = (await res.json()) as { dossiers?: unknown };
  expect(body.dossiers).toBeTruthy();
});

test("legal pages render", async ({ page }) => {
  await page.goto("/termos");
  await expect(page.locator("h1")).toBeVisible();
  await page.goto("/privacidade");
  await expect(page.locator("h1")).toBeVisible();
});

test("admin surface answers only to credentials", async ({ page }) => {
  // Same guard evidence as smoke.sh: a 200 means the operator surface
  // lost its guard — the posture is unsafe and the run must fail.
  const llm = await page.request.get("/api/v1/admin/llm");
  expect([401, 403]).toContain(llm.status());
  const ingestion = await page.request.get("/api/v1/admin/ingestion");
  expect([403, 404]).toContain(ingestion.status());
});

test("resend webhook refuses unsigned events", async ({ page }) => {
  // mail_webhook=configured is a posture requirement, so an unsigned
  // event must verify-fail (401), never reach provider handling.
  const res = await page.request.post("/webhooks/resend", { data: {} });
  expect(res.status()).toBe(401);
});

test("claim gate: creating a Radar is refused before any claim", async ({
  page,
}) => {
  // The one mutating-looking probe — same evidence as the smoke.sh
  // live-safe path: the server must refuse the write at the claim gate
  // (403 email_required) before any row or mail exists. A 2xx here
  // means the gate is off and the target is unsafe.
  const proposal = (await (
    await page.request.get("/api/v1/proposal")
  ).json()) as { proposal: Record<string, unknown> };
  const p = proposal.proposal;
  const res = await page.request.post("/api/v1/publications", {
    data: {
      name: p.name,
      topic: p.topic,
      audience: p.audience,
      editorial_line: p.editorial_line,
      tone: p.tone,
      language: p.language,
      frequency: p.frequency,
      ai_disclosure: p.ai_disclosure,
      sections: p.sections,
      paywall: p.paywall,
    },
  });
  expect(res.status()).toBe(403);
  expect(((await res.json()) as { error?: { code?: string } }).error?.code).toBe(
    "email_required",
  );
});

test("publishing-off surface stays hidden", async ({ page }) => {
  // The shipped posture (KLOX-122 pivot) hides the reader-facing
  // surface: send, outbox and /p pages must 404 before any workspace is
  // resolved. If publishing is re-enabled this test fails loudly — a
  // posture change the live-safe contract has to be re-reviewed for.
  const ws = await workspaceInfo(page);
  expect(ws.publishing).toBe(false);
  const send = await page.request.post("/api/v1/issues/1/send", {
    data: { audience: "readers", when: "now" },
  });
  expect(send.status()).toBe(404);
  for (const path of [
    "/api/v1/issues/1/after",
    "/api/v1/publications/1/outbox",
    "/p/radar",
    "/api/v1/p/radar",
    "/unsub",
  ]) {
    const res = await page.request.get(path);
    expect(res.status(), `GET ${path}`).toBe(404);
  }
});
