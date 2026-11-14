import { expect, test, type Page } from "@playwright/test";
import {
  fakeLive,
  fakeRequests,
  preflight,
  setFakeFailing,
} from "./mail.ts";

// Fallback coverage (KLOX-32): provider failure is tested as its own
// intentional state — never as a tolerated outcome of a success check.
// A forced 500 must land the message in the outbox ledger as
// status=fallback while the issue stays approved; recovering must send
// for real against the synthetic provider.
//
// API-driven so the assertion stays focused; fake-live mode only.

test.beforeEach(async ({ page }) => {
  await preflight(page);
});

const apiGet = async (page: Page, path: string) => {
  const res = await page.request.get(path);
  expect(res.ok(), `${path} -> ${res.status()}`).toBeTruthy();
  return res.json();
};
const apiPost = async (page: Page, path: string, data?: unknown, status = 200) => {
  const res = await page.request.post(path, { data: data ?? {} });
  expect(res.status(), `${path} -> ${res.status()}`).toBe(status);
  return res.json();
};

// waitRun polls a pipeline run to completion and returns the issue id.
async function waitRun(page: Page, rid: number) {
  for (let i = 0; i < 90; i++) {
    const { run } = await apiGet(page, `/api/v1/runs/${rid}`);
    if (run.status === "done") return run.issue_id as number;
    if (run.status === "failed") throw new Error(`run ${rid} failed: ${run.error}`);
    await page.waitForTimeout(2000);
  }
  throw new Error(`run ${rid} did not finish`);
}

// approvedIssue drives the API path to an approved issue — the same
// choreography smoke.sh uses, minus the browser.
async function approvedIssue(page: Page): Promise<number> {
  await apiPost(page, "/api/v1/demo/new");
  const proposal = (await apiGet(page, "/api/v1/proposal")).proposal;
  const pub = await apiPost(
    page,
    "/api/v1/publications",
    {
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
    201,
  );
  const pid = pub.publication.id as number;
  const catalog = await apiGet(page, `/api/v1/publications/${pid}/sources/catalog`);
  await apiPost(page, `/api/v1/publications/${pid}/sources/confirm`, {
    include: catalog.offered.map((o: { url: string }) => o.url),
  });
  const run = await apiPost(page, `/api/v1/publications/${pid}/runs`, {}, 202);
  const iid = await waitRun(page, run.run.id);
  const draft = await apiPost(page, `/api/v1/issues/${iid}/draft`, {}, 202);
  await waitRun(page, draft.run.id);
  // resolve flagged sentences and required commentary like the editor
  for (let i = 0; i < 30; i++) {
    const { document: doc } = await apiGet(page, `/api/v1/issues/${iid}/document`);
    const flagged = doc.sections
      .flatMap((s: { items: { sentences: { id: string; flag?: unknown }[] }[] }) => s.items)
      .flatMap((it) => it.sentences)
      .find((sn) => sn.flag);
    if (!flagged) break;
    await page.request.delete(`/api/v1/issues/${iid}/sentences/${flagged.id}`, {
      data: { version: doc.version },
    });
  }
  for (let i = 0; i < 10; i++) {
    const { document: doc } = await apiGet(page, `/api/v1/issues/${iid}/document`);
    const missing = doc.sections.find(
      (s: { commentary: { mode: string; text: string }; title: string }) =>
        s.commentary.mode === "required" && !s.commentary.text.trim(),
    );
    if (!missing) break;
    const res = await page.request.put(`/api/v1/issues/${iid}/commentary`, {
      data: {
        section: missing.title,
        text: "Reviewed for the fallback check.",
        version: doc.version,
      },
    });
    expect(res.ok()).toBeTruthy();
  }
  const readiness = await apiGet(page, `/api/v1/issues/${iid}/readiness`);
  expect(readiness.ready, `not ready: ${JSON.stringify(readiness.checks)}`).toBe(true);
  await apiPost(page, `/api/v1/issues/${iid}/approve`);
  return iid;
}

async function claimSynthetic(page: Page) {
  const claim = await apiPost(page, "/api/v1/workspace/claim", { email: "smoke@kloxus.dev" });
  expect(claim.delivery).toBe("resend");
  const pubs = await apiGet(page, "/api/v1/publications");
  const outbox = await apiGet(page, `/api/v1/publications/${pubs.publications[0].id}/outbox`);
  const ml = outbox.emails.find((e: { kind: string }) => e.kind === "magic_link");
  expect(ml).toBeTruthy();
  const mail = await apiGet(page, `/api/v1/outbox/${ml.id}`);
  const link = mail.email.body_md.match(/\/claim\?t=[A-Za-z0-9_-]+/)?.[0];
  expect(link).toBeTruthy();
  const confirmed = await page.request.get(link!);
  expect(confirmed.status()).toBe(200);
}

test("provider failure falls back to the outbox — and recovery sends", async ({ page }) => {
  test.skip(!fakeLive, "fake-live mode only");
  const iid = await approvedIssue(page);
  await claimSynthetic(page);

  // intentional failure: the provider must not count as success — the
  // message lands in the ledger as fallback and the issue stays
  // approved.
  await setFakeFailing(page, true);
  try {
    const failed = await apiPost(page, `/api/v1/issues/${iid}/send`, {
      audience: "test",
      when: "now",
    });
    expect(failed.delivery).toBe("outbox_fallback");
    expect(failed.fallback).toBe(true);
    expect(failed.issue.status).toBe("approved");
    const fmail = await apiGet(page, `/api/v1/outbox/${failed.outbox_email_id}`);
    expect(fmail.email.provider).toBe("resend");
    expect(fmail.email.status).toBe("fallback");
    expect(fmail.email.error).toBeTruthy();
  } finally {
    await setFakeFailing(page, false);
  }

  // recovered: the retry must really reach the synthetic provider —
  // receipt, recipients and ledger all agree.
  const before = (await fakeRequests(page)).count;
  const sent = await apiPost(page, `/api/v1/issues/${iid}/send`, {
    audience: "test",
    when: "now",
  });
  expect(sent.delivery).toBe("resend");
  expect(sent.sent).toBe(1);
  expect(sent.to).toEqual(["smoke@kloxus.dev"]);
  expect(sent.issue.status).toBe("approved");
  const reqs = await fakeRequests(page);
  expect(reqs.count - before).toBe(1);
  const last = reqs.requests.at(-1)!;
  expect(last.to).toEqual(["smoke@kloxus.dev"]);
  expect(last.auth).toBe("Bearer re_local_verify_synthetic");
  const smail = await apiGet(page, `/api/v1/outbox/${sent.outbox_email_id}`);
  expect(smail.email.provider).toBe("resend");
  expect(smail.email.status).toBe("sent");
  expect(smail.email.provider_id).toMatch(/^re_local_/);
});
