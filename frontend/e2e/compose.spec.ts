import { ensureCited, ensureNotCited } from "./onboard.ts";
import { expect, test, type Page } from "@playwright/test";
import {
  claimIfSynthetic,
  fakeLive,
  fakeRequests,
  neverRealMail,
  preflight,
  workspaceInfo,
} from "./mail.ts";
import { PUBLISHING_SKIP_REASON, publishingOff } from "./publishing.ts";

test.skip(publishingOff, PUBLISHING_SKIP_REASON);

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

const issueCol = (page: Page) => page.locator('section[aria-label="This piece"]');
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
  await expect(page.getByRole("heading", { name: /The research is ready/ })).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: /Create your Radar/ }).first().click();
  await page.waitForURL(/\/new/);
  await expect(page.locator('input[value*="Futuro"]')).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "Next", exact: true }).click();
  // both steps now share the one "Next" label — wait for the section
  // step to render so the second click can't land on page 1's button
  await expect(page.getByText(/Your piece will have \d+ stor/)).toBeVisible();
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.waitForURL(/\/publications\/\d+\/sources/);
  await expect(page.getByText("Suggested sources")).toBeVisible();
  await page.getByRole("button", { name: "Latest items", exact: true }).first().click();
  await expect(page.locator("ul li a").first()).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.waitForURL(/\/issues\/\d+/, { timeout: 180000 });
}

async function boardReady(page: Page) {
  await expect(page.getByRole("heading", { name: "This piece" })).toBeVisible({ timeout: 120000 });
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
  const btns = page.getByRole("button", { name: "Finish review" });
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
  await benchCol(page).getByRole("button", { name: "Add to piece", exact: true }).first().click();
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
  await emptySlot.first().click();
  await expect(box).toBeVisible();
  await box
    .getByRole("textbox")
    .fill("The Central Bank will end Pix in March 2027 and charge 9 reais per transfer.");
  await ensureCited(box, firstSrc.cite);
  await box.getByRole("button", { name: "Add to story" }).click();
  await expect(page.getByText(/not supported by \[\d+\]/).first()).toBeVisible({ timeout: 15000 });
  // the flag carries Edit/Remove actions — removing resolves it
  const fabricated = page.locator("[data-sid]").filter({ hasText: "Central Bank" });
  await fabricated.getByRole("button", { name: "Remove" }).click();
  await expect(fabricated).toHaveCount(0);
  // verbatim first-sentence paste with its citation
  await box.getByRole("textbox").fill(verbatimPaste(firstSrc.excerpt));
  await ensureCited(box, firstSrc.cite);
  await box.getByRole("button", { name: "Add to story" }).click();
  await expect(page.getByText("too close to the source").first()).toBeVisible({ timeout: 15000 });
  await page
    .locator("[data-sid]")
    .last()
    .getByRole("button", { name: "Remove" })
    .click();
  await expect(page.getByText("too close to the source")).toHaveCount(0);
  // the composer stays open for a next sentence on this pauta (KLOX-96
  // write-in-document) — close it so the slot walk below starts from
  // the first pauta, not the one still composing
  await box.getByRole("button", { name: "Cancel" }).click();
  await expect(box).toHaveCount(0);

  // write each item by hand: click the slot → the composer opens under
  // the item heading in the document (KLOX-96) → type a supported claim
  // → cite its source → add
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
    await ensureCited(box, cite);
    await box.getByRole("button", { name: "Add to story" }).click();
    // the sentence lands in the document as a cited fact
    await expect(page.locator("[data-sid]")).toHaveCount(i + 1, { timeout: 15000 });
    // close the composer between pautas: it stays open for a next
    // sentence on the same pauta, and an open composer suppresses that
    // pauta's empty slot — leaving it open would make the next
    // emptySlot.first() re-enter this pauta instead of the next one
    await box.getByRole("button", { name: "Cancel" }).click();
    await expect(box).toHaveCount(0);
  }
  // every slot filled — the empty affordance is gone
  await expect(emptySlot).toHaveCount(0);

  // audit reproduction (KLOX-52): a sentence added without picking a
  // source must land flagged exactly like an unsupported [n] — with a
  // visible reason and Edit/Remove — and "Review pending" must open the fix
  // affordance, not only flash the sentence. KLOX-96: the composer lives
  // under the item heading now — the pencil affordance opens it for the
  // FIRST item so the fix step's chips are firstSrc's.
  await page
    .locator("[data-item-ref]")
    .first()
    .getByRole("button", { name: "Add a sentence" })
    .click();
  await expect(box).toBeVisible();
  await box
    .getByRole("textbox")
    .fill("A claim nobody can trace back to a source.");
  // soleSource (#91) pre-selects the lone chip; this flow needs it off
  await ensureNotCited(box, firstSrc.cite);
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
  await ensureCited(uncited, firstSrc.cite);
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
    // the test send lives in the Actions menu (KLOX-101)
    (async () => {
      await page.getByRole("button", { name: "Actions" }).click();
      await page.getByRole("button", { name: "Send a test" }).click();
    })(),
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

// KLOX-96 design review (390px rework): the Escrever surface's phone
// tab bar used to render `position: static` as the last child of the
// page scroll — a full-document scroll away (y ≈ 4434 on a real
// edition) — and the per-heading pen / link-only controls measured
// 21×21px. The bar is now fixed just above the global bottom nav and
// the heading controls meet the 44px touch rule.
test("mobile 390px: Escrever tab bar stays pinned, heading controls ≥44px", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await onboard(page);
  // phone: the bench fills first; the issue column sits behind its tab
  await expect(page.getByRole("button", { name: /This piece/ })).toBeVisible({ timeout: 120000 });
  await expect(benchCol(page).locator("p.font-doc").first()).toBeVisible({ timeout: 60000 });
  await page.getByRole("button", { name: /This piece/ }).click();
  await page.getByRole("button", { name: "Write it myself" }).click();
  const emptySlot = page.getByRole("button", { name: /Write this story/ });
  await expect(emptySlot.first()).toBeVisible({ timeout: 30000 });

  const tabbar = page.getByRole("navigation", { name: "Piece panes" });
  const globalNav = page.getByRole("navigation", { name: "Radar" });
  await expect(tabbar).toBeVisible();

  // pinned flush above the global bottom nav — on screen at scroll-top
  // (the static bar sat ~3600px below the fold here) and never under it
  const assertPinned = async () => {
    const box = (await tabbar.boundingBox())!;
    const nav = (await globalNav.boundingBox())!;
    expect(box.y).toBeGreaterThan(0);
    expect(box.y + box.height).toBeLessThanOrEqual(nav.y + 1);
    expect(box.y + box.height).toBeGreaterThan(nav.y - 4);
    return box.y;
  };
  const yAtTop = await assertPinned();

  // write one supported sentence so the item heading gains its
  // per-heading controls — the pen only shows on non-empty pautas
  const iid = page.url().match(/\/issues\/(\d+)/)![1];
  const doc0 = (
    await (await page.request.get(`/api/v1/issues/${iid}/document`)).json()
  ).document;
  let srcExcerpt = "";
  let srcRef = 0;
  for (const s of doc0.sections) {
    for (const it of s.items) {
      const src = (it.sources ?? []).find((x: { excerpt?: string; ref: number }) => x.excerpt);
      if (src) {
        srcExcerpt = src.excerpt;
        srcRef = src.ref;
        break;
      }
    }
    if (srcExcerpt) break;
  }
  expect(srcExcerpt).toBeTruthy();
  await emptySlot.first().click();
  const box = page.locator('section[aria-label="Write this story"]');
  await expect(box).toBeVisible();
  await box.getByRole("textbox").fill(supportedSentence(srcExcerpt));
  await ensureCited(box, srcRef);
  await box.getByRole("button", { name: "Add to story" }).click();
  await expect(page.locator("[data-sid]").first()).toBeVisible({ timeout: 15000 });

  // mid-document: scroll the edition — the bar must not move with the
  // content
  await page.evaluate(() => {
    document.querySelector("main")!.scrollTop = 600;
  });
  expect(await assertPinned()).toBe(yAtTop);

  // the rail is one tap away and the bar stays pinned while it is shown
  await tabbar.getByRole("button", { name: "Sources and facts" }).click();
  await expect(page.locator(".ProseMirror").first()).toBeHidden();
  expect(await assertPinned()).toBe(yAtTop);
  await tabbar.getByRole("button", { name: "Piece", exact: true }).click();
  await expect(page.locator(".ProseMirror").first()).toBeVisible();

  // per-heading controls meet the 44px touch rule on phone (they were
  // 21×21px); the link-only toggle is reachable without hover too
  const pen = page.getByRole("button", { name: "Add a sentence" }).first();
  const link = page.getByRole("button", { name: "Ship as link only" }).first();
  await expect(pen).toBeAttached();
  await expect(link).toBeAttached();
  for (const [name, btn] of [["pen", pen], ["link-only", link]] as const) {
    const b = (await btn.boundingBox())!;
    expect(b.height, name).toBeGreaterThanOrEqual(44);
    expect(b.width, name).toBeGreaterThanOrEqual(44);
  }

  // no horizontal overflow at 390px
  const overflow = await page.evaluate(() => ({
    sw: document.documentElement.scrollWidth,
    iw: window.innerWidth,
  }));
  expect(overflow.sw).toBeLessThanOrEqual(overflow.iw);

  // desktop keeps the old flow — the phone bar never renders at ≥768px
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect(tabbar).toBeHidden();
});
