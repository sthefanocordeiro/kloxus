import { expect, request as playwrightRequest, type Page } from "@playwright/test";
import {
  assertLiveSafe,
  assertTransport,
  APP_ORIGIN,
  FAKE_ORIGIN,
  liveSafe as harnessLiveSafe,
  resolveMode,
} from "../../scripts/e2e-harness.mjs";

// Shared e2e mail/transport helpers (KLOX-32). Every spec runs a
// transport preflight first: the server's own /api/v1/workspace
// evidence decides whether a suite may proceed — a live or unknown
// transport fails closed before any mail- or model-capable call.

export const e2eMode = resolveMode();
export const fakeLive = e2eMode === "fake-live";
// E2E_LIVE_SAFE=1 (KLOX-176): the hosted opt-in that collects only the
// explicit read-only subset (live-safe.spec.ts) against a live-mail
// target. Mutating specs keep the assertTransport preflight and refuse
// the same target — nothing about this flag weakens them.
export const liveSafe = harnessLiveSafe();

export interface WorkspaceInfo {
  mail?: string;
  mail_transport?: string;
  mail_webhook?: string;
  mode?: string;
  hosted?: boolean;
  llm_mode?: string;
  claimed?: boolean;
  owner_email?: string;
  publishing?: boolean;
}

export async function workspaceInfo(page: Page): Promise<WorkspaceInfo> {
  const res = await page.request.get("/api/v1/workspace");
  expect(res.ok()).toBeTruthy();
  return (await res.json()) as WorkspaceInfo;
}

async function workspaceInfoIsolated(): Promise<WorkspaceInfo> {
  // /api/v1/workspace is resolve-or-create: querying it through
  // page.request would seed the browser context's cookie jar and make
  // the next landing visit look like a returning session (KLOX-288).
  // The preflight only needs posture evidence, so keep that throwaway
  // workspace cookie in an isolated request context and dispose it.
  const isolated = await playwrightRequest.newContext({
    baseURL: process.env.E2E_BASE_URL ?? APP_ORIGIN,
  });
  try {
    const res = await isolated.get("/api/v1/workspace");
    expect(res.ok()).toBeTruthy();
    return (await res.json()) as WorkspaceInfo;
  } finally {
    await isolated.dispose();
  }
}

// preflight gates a whole test on positive server-side evidence of a
// safe transport: hosted/local suites need an outbox transport, the
// fake-live suite needs the loopback synthetic one. Call it before any
// mail- or model-capable action; it never touches the browser context's
// cookies, so onboarding still starts as a first-time visitor.
export async function preflight() {
  assertTransport(await workspaceInfoIsolated(), e2eMode);
}

// preflightLiveSafe gates only the live-safe.spec.ts read-only subset:
// the target must prove the real-app live-mail posture (assertLiveSafe
// in the harness). The same cookie isolation keeps the landing check a
// first-visit check.
export async function preflightLiveSafe() {
  assertLiveSafe(await workspaceInfoIsolated());
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

// The address claimOwner verifies by default — test sends and reader
// sends to a claimed workspace deliver to it (KLOX-161).
export const OWNER_EMAIL = "owner@example.com";

export interface ClaimResult {
  delivery?: string;
  claim_url?: string;
  outbox_email_id?: number;
}

// claimOwner confirms the workspace owner's email by magic link —
// creating a Radar requires it (KLOX-161). Outbox mode returns the link
// in the claim response; the synthetic loopback mails it to the ledger.
// A real provider is refused: the claim email would leave the app.
// Returns the claim response (null when the workspace was already
// claimed) so callers can assert on transport evidence like delivery.
export async function claimOwner(page: Page, email = OWNER_EMAIL): Promise<ClaimResult | null> {
  const ws = await workspaceInfo(page);
  neverRealMail(ws);
  if (ws.claimed) return null;
  const claim = await page.request.post("/api/v1/workspace/claim", {
    data: { email },
  });
  expect(claim.ok()).toBeTruthy();
  const body = (await claim.json()) as ClaimResult;
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
  return body;
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
