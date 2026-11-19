import { expect, test, type Page } from "@playwright/test";
import {
  claimIfSynthetic,
  fakeLive,
  fakeRequests,
  neverRealMail,
  preflight,
  workspaceInfo,
} from "./mail.ts";

// Manual compose journey (KLOX-32): pick candidates → "Write it myself"
// opens the issue as empty item slots → the editor writes each pauta by
// hand with cited sources → approve → dry send. No draft run, no model
// step anywhere in the flow.

type EditorWindow = Window & {
  __ed?: {
    state: {
      selection: {
        empty: boolean;
        $from?: { node(n: number): { type?: { name?: string } } };
      };
    };
  };
};

const issueCol = (page: Page) => page.locator('section[aria-label="This issue"]');
const benchCol = (page: Page) => page.locator('section[aria-label="Suggestions"]');
const sectionBox = (page: Page, name: string) =>
  issueCol(page).locator(`[data-section="${name}"]`);

// A supported claim: content words of the source excerpt picked at
// non-consecutive positions — the citation floor verifies it and no
// copied run can form (KLOX-51).
function supportedSentence(excerpt: string): string {
  const words = (excerpt.match(/[\p{L}\p{N}]+/gu) ?? []).filter((w) => w.length >= 4);
  return words.filter((_, i) => i % 3 === 0).slice(0, 6).join(" ") + ".";
}

// The audit's first-sentence paste: the excerpt's opening words copied
// one for one — long enough to trip the consecutive-word copy rule.
function verbatimPaste(excerpt: string): string {
  const words = (excerpt.match(/[\p{L}\p{N}]+/gu) ?? []);
  return words.slice(0, 12).join(" ") + ".";
}

// same onboarding as the demo journey: landing → /new → sources → the
// first run lands on the build board
async function onboard(page: Page) {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: /The research is done/ })).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: /Create your newsletter/ }).first().click();
  await page.waitForURL(/\/new/);
  await expect(page.locator('input[value*="Futuro"]')).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "Next: sections", exact: true }).click();
  await page.getByRole("button", { name: "Continue to sources", exact: true }).click();
  await page.waitForURL(/\/publications\/\d+\/sources/);
  await expect(page.getByText("Suggested sources")).toBeVisible();
  await page.getByRole("button", { name: "Latest items", exact: true }).first().click();
  await expect(page.locator("ul li a").first()).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: /Confirm \d+ sources & collect/ }).click();
  await page.waitForURL(/\/issues\/\d+/, { timeout: 180000 });
}

async function boardReady(page: Page) {
  await expect(page.getByRole("heading", { name: "This issue" })).toBeVisible({ timeout: 120000 });
  await expect(benchCol(page).locator("p.font-doc").first()).toBeVisible({ timeout: 60000 });
}

// A bench "+" add either lands the story in its suggested section — or,
// when that section is full (deterministic ranking fills every slot),
// opens the chooser: pick a section, and picking a full one turns the
// same dialog into the swap picker ("…is full — pick a story to swap
// out"). A direct add toasts "Added", a swap toasts "Moved".
async function settleBenchAdd(page: Page) {
  const chooser = page
    .getByRole("dialog")
    .filter({ has: page.getByRole("button", { name: "Close" }) });
  const settled = page.getByText(/^(Added|Moved) “/);
  await expect(chooser.or(settled)).toBeVisible({ timeout: 15000 });
  if (!(await chooser.isVisible())) return;
  // the dialog's ✕ Close sits outside the list; section rows show
  // "n/cap" and full ones add "· full" — prefer a section with room
  const picks = chooser.getByRole("button").filter({ hasNotText: "✕" });
  const open = picks.filter({ hasNotText: "full" });
  if ((await open.count()) > 0) {
    await open.first().click();
  } else {
    // every section full: picking one opens the swap picker — the pick
    // then asks why it leaves; "just swap" settles without a reason
    await picks.first().click();
    await expect(chooser.getByText(/pick a story to swap out/)).toBeVisible();
    await picks.first().click();
    await chooser.getByRole("button", { name: "just swap" }).click();
  }
  await expect(settled).toBeVisible({ timeout: 15000 });
}

async function typeCommentary(page: Page) {
  const blocks = page.locator("[data-commentary]");
  await expect(blocks.first()).toBeVisible({ timeout: 30000 });
  const n = await blocks.count();
  for (let i = 0; i < n; i++) {
    const editable = blocks.nth(i).locator("[data-node-view-content]");
    await editable.click();
    await page.waitForFunction(() => {
      const s = (window as EditorWindow).__ed?.state.selection;
      return !!s && s.empty && s.$from?.node(-1)?.type?.name === "commentary";
    }, null, { timeout: 15000 });
    await page.keyboard.type("My reading of this section, for the record.", { delay: 15 });
  }
  await expect(page.getByText("Saved")).toBeVisible({ timeout: 15000 });
}

async function approveIssue(page: Page) {
  const btns = page.getByRole("button", { name: "Approve issue" });
  await btns.first().click();
  await expect(btns.nth(1)).toBeVisible({ timeout: 30000 });
  await btns.nth(1).click();
  const dialog = page
    .getByRole("dialog")
    .filter({ has: page.getByRole("button", { name: "Cancel" }) });
  await dialog.getByRole("button", { name: "Approve & send" }).click();
  await expect(page.getByText("approved", { exact: false }).first()).toBeVisible({ timeout: 15000 });
}

// Transport preflight first: positive server-side evidence decides
// whether this suite may run — a live or unknown transport fails here,
// before the test body performs a single mail- or model-capable call.
test.beforeEach(async ({ page }) => {
  await preflight(page);
});

test("manual compose journey", async ({ page }) => {
  await page.setViewportSize({ width: 1360, height: 850 });
  await onboard(page);
  await boardReady(page);

  // pick candidates: drop an AI suggestion and pull one story in from
  // the bench — the manual flow must also carry a story with no dossier
  const wk = sectionBox(page, "Vale saber");
  await wk.getByRole("button", { name: "Remove", exact: true }).first().click();
  await page.getByRole("button", { name: "just remove" }).click();
  await benchCol(page).getByRole("button", { name: "Add to issue", exact: true }).first().click();
  await settleBenchAdd(page);

  // manual compose: the lineup becomes empty slots — no draft run
  await page.getByRole("button", { name: "Write it myself" }).click();
  const emptySlot = page.getByRole("button", { name: /Write this story/ });
  await expect(emptySlot.first()).toBeVisible({ timeout: 30000 });
  const total = await emptySlot.count();
  expect(total).toBeGreaterThan(0);

  // The citation floor verifies every composed sentence against the
  // cited source's stored text (KLOX-51) — supported claims are built
  // from each item's own source excerpt.
  const iid = page.url().match(/\/issues\/(\d+)/)![1];
  const doc0 = (
    await (await page.request.get(`/api/v1/issues/${iid}/document`)).json()
  ).document;
  const supported = new Map<number, { text: string; cite: number; excerpt: string }>();
  for (const s of doc0.sections) {
    for (const it of s.items) {
      const src = (it.sources ?? []).find((x: { excerpt?: string; ref: number }) => x.excerpt);
      if (src) {
        supported.set(it.ref, {
          text: supportedSentence(src.excerpt),
          cite: src.ref,
          excerpt: src.excerpt,
        });
      }
    }
  }
  const box = page.locator('section[aria-label="Write this story"]');
  const itemRefOf = async () =>
    Number(
      await emptySlot
        .first()
        .evaluate((el) => (el.closest("[data-item-ref]") as HTMLElement).dataset.itemRef),
    );

  // audit reproduction (KLOX-51): a fabricated claim carrying an
  // unrelated [n] lands flagged with a visible reason — never as
  // "sourced" — and the same goes for a verbatim source paste.
  const firstRef = await itemRefOf();
  const firstSrc = supported.get(firstRef)!;
  const firstChip = new RegExp(`^\\[${firstSrc.cite}\\] `);
  await emptySlot.first().click();
  await expect(box).toBeVisible();
  await box
    .getByRole("textbox")
    .fill("The Central Bank will end Pix in March 2027 and charge 9 reais per transfer.");
  await box.getByRole("button", { name: firstChip }).click();
  await box.getByRole("button", { name: "Add to story" }).click();
  await expect(page.getByText(/not supported by \[\d+\]/).first()).toBeVisible({ timeout: 15000 });
  // the flag carries Edit/Remove actions — removing resolves it
  const fabricated = page.locator("[data-sid]").filter({ hasText: "Central Bank" });
  await fabricated.getByRole("button", { name: "Remove" }).click();
  await expect(fabricated).toHaveCount(0);
  // verbatim first-sentence paste with its citation
  await box.getByRole("textbox").fill(verbatimPaste(firstSrc.excerpt));
  await box.getByRole("button", { name: firstChip }).click();
  await box.getByRole("button", { name: "Add to story" }).click();
  await expect(page.getByText("too close to the source").first()).toBeVisible({ timeout: 15000 });
  await page
    .locator("[data-sid]")
    .last()
    .getByRole("button", { name: "Remove" })
    .click();
  await expect(page.getByText("too close to the source")).toHaveCount(0);

  // write each item by hand: click the slot → compose box in the
  // evidence pane → type a supported claim → cite its source → add
  // rendered markdown places the citation inside final punctuation —
  // "… quando [1]." — so compare without the trailing '.'
  let firstWritten = "";
  for (let i = 0; i < total; i++) {
    const itemRef = await itemRefOf();
    const { text, cite } = supported.get(itemRef)!;
    if (i === 0) firstWritten = text.replace(/\.$/, "");
    await emptySlot.first().click();
    await expect(box).toBeVisible();
    await box.getByRole("textbox").fill(text);
    await box.getByRole("button", { name: new RegExp(`^\\[${cite}\\] `) }).click();
    await box.getByRole("button", { name: "Add to story" }).click();
    // the sentence lands in the document as a cited fact
    await expect(page.locator("[data-sid]")).toHaveCount(i + 1, { timeout: 15000 });
  }
  // every slot filled — the empty affordance is gone
  await expect(emptySlot).toHaveCount(0);

  // audit reproduction (KLOX-52): a sentence added without picking a
  // source must land flagged exactly like an unsupported [n] — with a
  // visible reason and Edit/Remove — and "Review pending" must open the fix
  // affordance, not only flash the sentence.
  await page.locator("[data-item-ref] h3").first().click();
  await expect(box).toBeVisible();
  await box
    .getByRole("textbox")
    .fill("A claim nobody can trace back to a source.");
  await box.getByRole("button", { name: "Add to story" }).click();
  const uncited = page.locator("[data-sid]").filter({ hasText: "nobody can trace" });
  await expect(uncited.getByText("sentence has no citation")).toBeVisible({ timeout: 15000 });
  await expect(uncited.getByRole("button", { name: "Edit" })).toBeVisible();
  await expect(uncited.getByRole("button", { name: "Remove" })).toBeVisible();
  // "Review pending" opens the sentence's edit box — the citation chips are
  // the way out; resolving through them unblocks approval entirely in
  // the UI
  await page.getByRole("button", { name: "Review pending" }).first().click();
  await expect(uncited.getByRole("textbox")).toBeVisible({ timeout: 15000 });
  await uncited.getByRole("textbox").fill(firstSrc.text);
  await uncited.getByRole("button", { name: firstChip }).click();
  await uncited.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("sentence has no citation")).toHaveCount(0, { timeout: 15000 });

  await typeCommentary(page);
  await approveIssue(page);

  // live-mail rules gate sends behind a claimed workspace — claim
  // first (synthetic transport only; a real provider is refused), or
  // the test send is correctly refused 409 unclaimed_workspace
  await claimIfSynthetic(page);

  // dry send: a test email goes out, the issue stays approved. The
  // outcome must be the recorded send itself — never a provider
  // failure or fallback — and the recorded message must carry the
  // editor-written, cited body.
  neverRealMail(await workspaceInfo(page));
  const [sendResp] = await Promise.all([
    page.waitForResponse((r) => r.url().endsWith("/send") && r.request().method() === "POST"),
    // the approved header cluster's direct button (KLOX-80)
    page.getByRole("button", { name: "Send a test" }).click(),
  ]);
  const sendBody = await sendResp.json();
  expect(sendBody.issue.status).toBe("approved");
  if (fakeLive) {
    // synthetic-live: the fake provider must have accepted the message —
    // "Resend failed" or a fallback is a failure here, not a success
    expect(sendBody.delivery).toBe("resend");
    expect(sendBody.to).toEqual(["smoke@kloxus.dev"]);
    const reqs = await fakeRequests(page);
    const last = reqs.requests.at(-1)!;
    expect(last.to).toEqual(["smoke@kloxus.dev"]);
    expect(last.auth).toBe("Bearer re_local_verify_synthetic");
    expect(last.text).toContain(firstWritten);
    expect(last.text).toMatch(/\/a\/\d+/); // the citation link survives to the wire
  } else {
    expect(sendBody.delivery).toBe("outbox");
  }
  await expect(page.getByText(/^Test sent to /).first()).toBeVisible({ timeout: 15000 });
  const mail = await (
    await page.request.get(`/api/v1/outbox/${sendBody.outbox_email_id}`)
  ).json();
  expect(mail.email.body_md).toContain(firstWritten);
  if (fakeLive) {
    // the owner test send is ungated — citation links ship in the copy
    expect(mail.email.body_md).toMatch(/\/a\/\d+/);
    expect(mail.email.provider).toBe("resend");
    expect(mail.email.status).toBe("sent");
  } else {
    // the recorded copy is gated for the unsubscribed demo reader —
    // the paywall keeps the cited teaser sentence but strips the
    // **Sources:** lines and their /a/n links. Citation-link coverage
    // is asserted on the ungated subscriber preview instead.
    expect(mail.email.body_md).toMatch(/\[1\]/);
    expect(mail.email.body_md).toMatch(/Exclusivo para assinantes|Subscribers only/);
    const iid = page.url().match(/\/issues\/(\d+)/)![1];
    const prev = await (
      await page.request.get(`/api/v1/issues/${iid}/preview?reader=subscriber`)
    ).json();
    expect(prev.gated).toBe(false);
    expect(prev.body_md).toContain(firstWritten);
    expect(prev.body_md).toMatch(/\/a\/\d+/);
  }
});
