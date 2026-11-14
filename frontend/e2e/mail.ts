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
  const pubs = await (await page.request.get("/api/v1/publications")).json();
  const outbox = await (
    await page.request.get(`/api/v1/publications/${pubs.publications[0].id}/outbox`)
  ).json();
  const ml = outbox.emails.find((e: { kind: string }) => e.kind === "magic_link");
  expect(ml).toBeTruthy();
  const mail = await (await page.request.get(`/api/v1/outbox/${ml.id}`)).json();
  const link = mail.email.body_md.match(/\/claim\?t=[A-Za-z0-9_-]+/)?.[0];
  expect(link).toBeTruthy();
  // GET /claim sets the workspace cookie on this browser context
  const confirmed = await page.request.get(link!);
  expect(confirmed.status()).toBe(200);
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
