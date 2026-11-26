import { expect, test, type Page } from "@playwright/test";
import {
  claimOwner,
  fakeLive,
  fakeRequests,
  preflight,
  setFakeFailing,
} from "./mail.ts";
import { PUBLISHING_SKIP_REASON, publishingOff } from "./publishing.ts";

test.skip(publishingOff, PUBLISHING_SKIP_REASON);

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

// A supported claim: content words of the source excerpt picked at
// non-consecutive positions — the citation floor verifies it and no
// copied run can form (KLOX-51). Same helper as compose.spec.ts.
function supportedSentence(excerpt: string): string {
  const words = (excerpt.match(/[\p{L}\p{N}]+/gu) ?? []).filter((w) => w.length >= 4);
  return words.filter((_, i) => i % 3 === 0).slice(0, 6).join(" ") + ".";
}

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
async function approvedIssue(page: Page): Promise<{ iid: number; ownerEmail: string }> {
  await apiPost(page, "/api/v1/demo/new");
  // creating a radar needs the claimed owner (KLOX-161) — under the
  // synthetic provider the claim itself must report resend delivery
  const ownerEmail = `smoke-${Date.now()}-${Math.floor(Math.random() * 1e9)}@kloxus.dev`;
  const claim = await claimOwner(page, ownerEmail);
  expect(claim?.delivery).toBe("resend");
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
  // Under llm off the draft is KLOX-48's honest skeleton: items with
  // sources and empty body slots, so empty_items blocks approval
  // (KLOX-58). Fill each empty item the way the editor does — the
  // excerpt-derived supported fill compose/journey use: a claim built
  // from the item's own source excerpt, cited through the same compose
  // endpoint the "Write this story" box calls. A dossierless item with
  // no usable excerpt gets the editor's own take instead (no citation
  // needed), the same escape hatch journey.spec.ts uses.
  for (let i = 0; i < 40; i++) {
    const { document: doc } = await apiGet(page, `/api/v1/issues/${iid}/document`);
    const empty = doc.sections
      .flatMap(
        (s: {
          items: {
            ref: number;
            sentences: unknown[];
            sources?: { ref: number; excerpt?: string }[];
          }[];
        }) => s.items,
      )
      .find((it) => it.sentences.length === 0);
    if (!empty) break;
    const src = (empty.sources ?? []).find(
      (x): x is { ref: number; excerpt: string } => !!x.excerpt,
    );
    const text = src ? supportedSentence(src.excerpt) : "";
    const payload =
      src && text.length > 1
        ? { text, citations: [src.ref] }
        : { text: "Editor's reading of the sources.", kind: "interpretation" };
    await apiPost(page, `/api/v1/issues/${iid}/items/${empty.ref}/compose`, {
      ...payload,
      version: doc.version,
    });
  }
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
  return { iid, ownerEmail };
}

test("provider failure falls back to the outbox — and recovery sends", async ({ page }) => {
  test.skip(!fakeLive, "fake-live mode only");
  const { iid, ownerEmail } = await approvedIssue(page); // already claimed

  // intentional failure: the provider must not count as success — the
  // message lands in the ledger as fallback and the issue stays
  // approved.
  let failed;
  await setFakeFailing(page, true);
  try {
    failed = await apiPost(page, `/api/v1/issues/${iid}/send`, {
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
  // receipt, recipients and ledger all agree. A fallback row never
  // left the app, so it must not satisfy idempotency: the retry
  // requeues the same ledger row instead of deduping on it.
  const before = (await fakeRequests(page)).count;
  const sent = await apiPost(page, `/api/v1/issues/${iid}/send`, {
    audience: "test",
    when: "now",
  });
  expect(sent.delivery).toBe("resend");
  expect(sent.sent).toBe(1);
  expect(sent.to).toEqual([ownerEmail]);
  expect(sent.issue.status).toBe("approved");
  expect(sent.outbox_email_id).toBe(failed.outbox_email_id);
  const reqs = await fakeRequests(page);
  expect(reqs.count - before).toBe(1);
  const last = reqs.requests.at(-1)!;
  expect(last.to).toEqual([ownerEmail]);
  expect(last.auth).toBe("Bearer re_local_verify_synthetic");
  const smail = await apiGet(page, `/api/v1/outbox/${sent.outbox_email_id}`);
  expect(smail.email.provider).toBe("resend");
  expect(smail.email.status).toBe("sent");
  expect(smail.email.provider_id).toMatch(/^re_local_/);
});
