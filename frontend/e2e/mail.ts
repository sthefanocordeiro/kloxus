import { expect, type Page } from "@playwright/test";
import { assertTransport, FAKE_ORIGIN, resolveMode } from "../../scripts/e2e-harness.mjs";

// Shared e2e mail/transport helpers (KLOX-32). Every spec runs a
// transport preflight first: the server's own /api/v1/workspace
// evidence decides whether a suite may proceed — a live or unknown
// transport fails closed before any mail- or model-capable call.

export const e2eMode = resolveMode();
export const fakeLive = e2eMode === "fake-live";

export interface WorkspaceInfo {
  mail?: string;
  mail_transport?: string;
  mode?: string;
  llm_mode?: string;
  claimed?: boolean;
  owner_email?: string;
}

export async function workspaceInfo(page: Page): Promise<WorkspaceInfo> {
  const res = await page.request.get("/api/v1/workspace");
  expect(res.ok()).toBeTruthy();
  return (await res.json()) as WorkspaceInfo;
}

// preflight gates a whole test on positive server-side evidence of a
// safe transport: hosted/local suites need an outbox transport, the
// fake-live suite needs the loopback synthetic one. Call it from
// test.beforeEach so a wrong or live target fails before the test body
// performs a single mail- or model-capable action.
export async function preflight(page: Page) {
  assertTransport(await workspaceInfo(page), e2eMode);
}

// neverRealMail is the in-body guard for helpers that must never touch
// a real provider: a live transport with a non-synthetic identity is an
// error even if the preflight was skipped.
export function neverRealMail(ws: WorkspaceInfo) {
  if (ws.mail === "live" && ws.mail_transport !== "synthetic") {
    throw new Error(
      `refusing to run a mail-capable step against transport '${ws.mail_transport}' — only the synthetic loopback fake may send`,
    );
  }
}

// claimIfSynthetic completes the workspace claim via the magic link in
// the outbox — only under the synthetic loopback transport. Outbox mode
// has no claim gate; a real provider is refused.
export async function claimIfSynthetic(page: Page) {
  const ws = await workspaceInfo(page);
  neverRealMail(ws);
  if (ws.mail !== "live" || ws.claimed) return;
  const claim = await page.request.post("/api/v1/workspace/claim", {
    data: { email: "smoke@kloxus.dev" },
  });
  expect(claim.ok()).toBeTruthy();
  // The publication outbox listing hides account-level magic-link rows
  // (KLOX-66 §8) — the claim response's outbox_email_id is the direct
  // handle to the ledger row carrying the link.
  const { outbox_email_id: eid } = (await claim.json()) as {
    outbox_email_id?: number;
  };
  expect(eid).toBeTruthy();
  const mail = await (await page.request.get(`/api/v1/outbox/${eid}`)).json();
  expect(mail.email.kind).toBe("magic_link");
  const link = mail.email.body_md.match(/\/claim\?t=[A-Za-z0-9_-]+/)?.[0];
  expect(link).toBeTruthy();
  // Two-step claim: GET lands the confirm page (prefetch-safe); the
  // explicit POST spends the token and sets the cookie on this context
  const claimPage = await page.request.get(link!);
  expect(claimPage.status()).toBe(200);
  const confirmed = await page.request.post(link!);
  expect(confirmed.status()).toBe(200);
}

// claimOwner confirms the workspace owner's email by magic link —
// creating a Radar requires it (KLOX-161). Outbox mode returns the link
// in the claim response; the synthetic loopback mails it to the ledger.
// A real provider is refused: the claim email would leave the app.
export async function claimOwner(page: Page) {
  const ws = await workspaceInfo(page);
  neverRealMail(ws);
  if (ws.claimed) return;
  const claim = await page.request.post("/api/v1/workspace/claim", {
    data: { email: "owner@example.com" },
  });
  expect(claim.ok()).toBeTruthy();
  const body = (await claim.json()) as { claim_url?: string; outbox_email_id?: number };
  let link = body.claim_url;
  if (!link) {
    expect(body.outbox_email_id).toBeTruthy();
    const mail = await (await page.request.get(`/api/v1/outbox/${body.outbox_email_id}`)).json();
    link = mail.email.body_md.match(/\/claim\?t=[A-Za-z0-9_-]+/)?.[0];
  }
  expect(link).toBeTruthy();
  const u = new URL(link!, "http://x.invalid");
  const path = u.pathname + u.search;
  // two-step claim: GET lands the confirm page, POST spends the token
  expect((await page.request.get(path)).status()).toBe(200);
  expect((await page.request.post(path)).status()).toBe(200);
}

export interface FakeRequest {
  n: number;
  method: string;
  path: string;
  auth: string;
  idem: string;
  to: string[] | null;
  from: string | null;
  subject: string | null;
  text: string | null;
}

// fakeRequests reads the synthetic provider's receipt log — positive
// proof of what actually crossed the wire (counts, recipients, auth,
// payload). Only reachable in fake-live mode.
export async function fakeRequests(page: Page): Promise<{ count: number; requests: FakeRequest[] }> {
  const res = await page.request.get(`${FAKE_ORIGIN}/requests`);
  expect(res.ok()).toBeTruthy();
  return res.json();
}

// setFakeFailing toggles the fake's intentional provider failure —
// fallback is always tested as a separate, deliberate state.
export async function setFakeFailing(page: Page, on: boolean) {
  const res = await page.request.post(`${FAKE_ORIGIN}/__fail`, { data: { on } });
  expect(res.ok()).toBeTruthy();
}
