import { expect, test, type Page } from "@playwright/test";
import {
  claimIfSynthetic,
  fakeRequests,
  neverRealMail,
  preflight,
  workspaceInfo,
} from "./mail.ts";

// The judge path at both widths: landing → cited sample → create the
// sample newsletter → sources → auto first run → build board → remove
// w/ reason → add from the bench → move between sections → draft → fix
// the flag → commentary → approve → schedule + hold → send → reader.

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
// copied run can form (KLOX-51). Same helper as compose.spec.ts.
function supportedSentence(excerpt: string): string {
  const words = (excerpt.match(/[\p{L}\p{N}]+/gu) ?? []).filter((w) => w.length >= 4);
  return words.filter((_, i) => i % 3 === 0).slice(0, 6).join(" ") + ".";
}

// KLOXUS_LLM_MODE=off runs the whole pipeline deterministically — zero
// model calls. The guide rebuild behind "Rule learned" is not a model
// call: reasons condense deterministically, so the rule persists in
// every mode (KLOX-55). The workspace endpoint reports the mode; a
// server that predates the flag returns no field and behaves like
// "auto".
async function llmOff(page: Page) {
  const res = await page.request.get("/api/v1/workspace");
  if (!res.ok()) return false;
  const body = (await res.json()) as {
    llm_mode?: string;
    workspace?: { llm_mode?: string };
  };
  return (body.workspace ?? body).llm_mode === "off";
}

// The workspace starts empty — every journey walks onboarding first:
// landing → /new → /pub/:id/sources → confirm → first run → board.
async function onboard(page: Page) {
  await page.goto("/");
  // the landing renders the hero headline before any workspace exists
  await expect(page.getByRole("heading", { name: /The research is done/ })).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: /Create your newsletter/ }).first().click();
  await page.waitForURL(/\/new/);

  // the sample proposal pre-fills the five editable blocks
  await expect(page.locator('input[value*="Futuro"]')).toBeVisible({ timeout: 15000 });
  // step 2 is the section builder (KLOX-65): keep the classic template
  // the proposal suggested — the edition totals travel with it
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await expect(page.getByText(/Your edition will have \d+ stor/)).toBeVisible();
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.waitForURL(/\/publications\/\d+\/sources/);

  // the sources screen offers the fixture sources — preview one, then
  // confirm all: the first collection runs automatically into the board
  await expect(page.getByText("Suggested sources")).toBeVisible();
  await page.getByRole("button", { name: "Latest items", exact: true }).first().click();
  await expect(page.locator("ul li a").first()).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "Next", exact: true }).click();
  // the first real-mode run collects, dedupes and triages live feeds —
  // ~10-13 min hosted, seconds against fixtures
  await page.waitForURL(/\/issues\/\d+/, { timeout: process.env.E2E_BASE_URL ? 840_000 : 600_000 });
}

async function boardReady(page: Page, isPhone: boolean) {
  // the step rail finishes, then the board lands. On phone the issue
  // column sits behind its tab — the tab bar is the ready signal there.
  await expect(
    isPhone
      ? page.getByRole("button", { name: /This issue/ })
      : page.getByRole("heading", { name: "This issue" }),
    // hosted runs the real pipeline (~10 min); local fixtures land fast
  ).toBeVisible({ timeout: process.env.E2E_BASE_URL ? 840_000 : 120_000 });
  // hosted: the bench fills when the real run ends (~10-13 min)
  await expect(benchCol(page).locator("p.font-doc").first()).toBeVisible({
    timeout: process.env.E2E_BASE_URL ? 840_000 : 60_000,
  });
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

async function boardOps(page: Page, isPhone: boolean) {
  const off = await llmOff(page);
  const tabIssue = page.getByRole("button", { name: /This issue/ });
  const tabBench = page.getByRole("button", { name: /^Suggestions/ });
  if (isPhone) await tabIssue.click();

  // 1) remove a Worth-knowing story giving a reason — the guide learns
  //    a rule in every mode: under llm off the reason condenses
  //    deterministically with no model call (KLOX-55), so the toast
  //    always reads "Rule learned". The bench badge still marks the
  //    deterministic run (attached-asserted: the bench column is hidden
  //    behind its tab on phone)
  const wk = sectionBox(page, "Vale saber");
  const removedTitle = (await wk.locator("p.font-doc").first().textContent())!.trim();
  await wk.getByRole("button", { name: "Remove", exact: true }).first().click();
  await page.getByRole("button", { name: "not for my audience" }).click();
  await expect(page.getByText(/Rule learned/)).toBeVisible({ timeout: 15000 });
  // 2) add a story from the bench with the "+" button (nth(1): the
  //    rejected story is off the bench entirely — pick the second
  //    suggestion so the persistence check below stays meaningful). A
  //    full suggested section opens the section chooser instead of
  //    failing.
  if (isPhone) await tabBench.click();
  await benchCol(page).getByRole("button", { name: "Add to issue", exact: true }).nth(1).click();
  await settleBenchAdd(page);

  // 3) move a Worth-knowing story into Quick links via the move menu —
  //    deterministic ranking caps Quick links 5/5, so under llm off the
  //    move opens the swap picker: pick the story that leaves. The move
  //    popover stays open under the dialog, so scope by the prompt text.
  if (isPhone) await tabIssue.click();
  await sectionBox(page, "Vale saber")
    .getByRole("button", { name: /Move to/ })
    .first()
    .click();
  await page.getByRole("button", { name: /Links rápidos/ }).click();
  if (off) {
    const swap = page.getByRole("dialog").filter({ hasText: /pick a story to swap out/ });
    await expect(swap).toBeVisible({ timeout: 15000 });
    await swap.getByRole("button").filter({ hasNotText: "✕" }).first().click();
    // picking the story that leaves asks why — "just swap" skips the
    // reason and the guide stays quiet. The pick re-renders the dialog
    // ("Why does … leave?"), so the `swap` locator above no longer
    // matches — look the button up inside whatever dialog is open
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "just swap" })
      .click();
  }
  await expect(page.getByText(/^Moved “/)).toBeVisible({ timeout: 15000 });

  // 4) the lineup persists across a reload — and the rejected story is
  //    gone from both the issue and the bench: a reasoned rejection is
  //    an editorial decision, not a suggestion (KLOX-55)
  await page.reload();
  await boardReady(page, isPhone);
  await expect(benchCol(page).getByText(removedTitle)).toHaveCount(0);
  if (isPhone) await tabIssue.click();

  // 5) draft — the step rail runs, then the editor lands. Under llm
  //    off the draft is KLOX-48's honest compose skeleton with 0/0
  //    sentences, so [data-sid] never appears: wait for the editor
  //    itself, then fill each empty item slot by hand the way
  //    compose.spec.ts does — a real draft lands with sentences and
  //    skips the fill
  await page.getByRole("button", { name: /Draft the issue/ }).click();
  await expect(page.locator(".ProseMirror").first()).toBeVisible({
    timeout: process.env.E2E_BASE_URL ? 840_000 : 600_000,
  });
  // The citation floor verifies every hand-written sentence against the
  // cited source's stored text (KLOX-51) — supported claims are built
  // from each item's own source excerpt, the same way compose.spec.ts
  // fills its slots.
  const iid = page.url().match(/\/issues\/(\d+)/)![1];
  const doc0 = (
    await (await page.request.get(`/api/v1/issues/${iid}/document`)).json()
  ).document;
  const supported = new Map<number, { text: string; cite: number }>();
  for (const s of doc0.sections ?? []) {
    for (const it of s.items ?? []) {
      const src = (it.sources ?? []).find((x: { excerpt?: string; ref: number }) => x.excerpt);
      if (src) supported.set(it.ref, { text: supportedSentence(src.excerpt), cite: src.ref });
    }
  }
  const emptySlot = page.getByRole("button", { name: /Write this story/ });
  for (let filled = 0, tries = 0; tries < 24; tries++) {
    if ((await emptySlot.count()) === 0) break;
    // each add refetches the document and remounts every node view — a
    // slot counted before the refetch lands detaches mid-click, so a
    // failed click just means "look again" (KLOX-49)
    let itemRef: number | undefined;
    try {
      itemRef = Number(
        await emptySlot
          .first()
          .evaluate((el) => (el.closest("[data-item-ref]") as HTMLElement).dataset.itemRef),
      );
      await emptySlot.first().click({ timeout: 10_000 });
    } catch {
      continue;
    }
    const fill = itemRef != null ? supported.get(itemRef) : undefined;
    const box = page.locator('section[aria-label="Write this story"]');
    await expect(box).toBeVisible();
    await box.getByRole("textbox").fill(fill?.text ?? "Editor's reading of the sources.");
    if (fill) {
      await box.getByRole("button", { name: new RegExp(`^\\[${fill.cite}\\] `) }).click();
      await box.getByRole("button", { name: "Add to story" }).click();
    } else {
      // no source excerpt to verify against — write the item as the
      // editor's own take so the slot still fills without a citation
      await box.getByRole("checkbox", { name: /my take/i }).check();
      await box.getByRole("button", { name: "Add to story" }).click();
    }
    // wait for the remounted document — the count check above is only
    // trustworthy once the refetch has landed (compose.spec.ts does the
    // same through [data-sid])
    await expect(page.locator("[data-sid]")).toHaveCount(filled + 1, { timeout: 15000 });
    filled++;
    // on phone the compose box lives behind the evidence tab — reopen
    // the issue tab so the next slot is reachable
    if (isPhone) await page.getByRole("button", { name: /^Issue$/ }).click();
  }

  // 6) the document is sealed: clicking a section or item heading and
  //    typing must not create free text anywhere — the schema only
  //    carries atoms + commentary
  const docEl = page.locator(".ProseMirror");
  const before = await docEl.first().textContent();
  // :visible skips a StrictMode ghost node-view that renders an empty h2
  const headings = docEl.locator("h2:visible, [data-item-ref]:visible");
  const n = await headings.count();
  for (let i = 0; i < n; i++) {
    // an item-heading click opens the evidence pane — on phone that
    // swaps tabs, so reopen the document before the next heading
    if (isPhone) await page.getByRole("button", { name: /^Issue$/ }).click();
    await headings.nth(i).click();
    await page.keyboard.type("XYZ", { delay: 10 });
  }
  await page.waitForTimeout(300);
  expect(await docEl.first().textContent()).toBe(before);
  await expect(docEl.getByText("XYZ")).toHaveCount(0);
  if (isPhone) await page.getByRole("button", { name: /^Issue$/ }).click();
}

async function clearFlags(page: Page) {
  // flagged sentences offer Edit / Mark as interpretation / Remove —
  // Remove is deterministic for the demo fixture. Removed sentences
  // linger briefly as disabled node views until the document refetch
  // rebuilds the editor, so click only enabled buttons and never let a
  // stale locator block the loop (it must exit on the all-clear, not
  // wait for a button that will never come back).
  const remove = page.getByRole("button", { name: "Remove", disabled: false });
  // exercise the UI path first — the judge sees a human resolving flags
  for (let i = 0; i < 12; i++) {
    if ((await remove.count()) === 0) break;
    try {
      await remove.first().click({ timeout: 10_000 });
    } catch {
      // the node detached mid-click — re-poll
    }
    await page.waitForTimeout(800);
  }
  if ((await remove.count()) > 0) {
    // real drafts flag or leave uncited hundreds of sentences — clearing
    // them one click at a time outruns any sane bound. Resolve the rest
    // through the same API the editor calls (smoke.sh does the same).
    const issueID = page.url().match(/\/issues\/(\d+)/)?.[1];
    for (let i = 0; i < 400 && issueID; i++) {
      const doc = (await (await page.request.get(`/api/v1/issues/${issueID}/document`)).json()).document;
      let sid: string | undefined;
      for (const s of doc.sections)
        for (const it of s.items)
          for (const sn of it.sentences)
            if (sn.flag || (sn.kind === "fact" && !(sn.citations && sn.citations.length))) sid = String(sn.id);
      if (!sid) break;
      const res = await page.request.delete(`/api/v1/issues/${issueID}/sentences/${sid}`, {
        data: { version: doc.version },
      });
      if (!res.ok() && res.status() !== 409) break;
    }
  }
  // every Remove — disabled zombie node views included — must be gone so
  // the last removal has settled and the editor rebuilt before the next
  // step; a pending DELETE would race the first commentary save (409).
  await expect(page.getByRole("button", { name: "Remove" })).toHaveCount(0, { timeout: 60_000 });
}

async function typeCommentary(page: Page) {
  // every required commentary block gets text; autosave must say Saved
  const blocks = page.locator("[data-commentary]");
  await expect(blocks.first()).toBeVisible({ timeout: 30000 });
  const n = await blocks.count();
  for (let i = 0; i < n; i++) {
    const editable = blocks.nth(i).locator("[data-node-view-content]");
    await editable.click();
    // ProseMirror folds the DOM caret into state.selection asynchronously
    // (selectionchange) — typing ahead of it hits the stale selection.
    // Wait until the caret is a text cursor inside this commentary node.
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
  // header trigger opens the readiness popover; its Approve button is
  // rendered only when every check is green — waiting for it also waits
  // out any readiness refetch race
  await btns.first().click();
  await expect(btns.nth(1)).toBeVisible({ timeout: 30000 });
  await btns.nth(1).click();
  // the popover closes when the confirm dialog grabs focus — identify
  // the dialog by its unique Cancel button (popover content is also
  // role=dialog in Radix)
  const dialog = page
    .getByRole("dialog")
    .filter({ has: page.getByRole("button", { name: "Cancel" }) });
  // the Approve & send dialog defaults to "Nobody (test only)" — the
  // confirm approves without sending to readers. Item rows already carry
  // "approved" chips, so assert on the post-approval Send control, not on
  // matching text — the approval round-trip can lag behind the click.
  await dialog.getByRole("button", { name: "Approve & send" }).click();
  await expect(page.getByRole("button", { name: "Send", exact: true })).toBeVisible({ timeout: 30000 });
}

// Etapa 3 keeps a single primary Send — test send, schedule, export
// and preview live in the Actions menu (KLOX-101). Each item closes
// the menu on activation, so open it per interaction.
async function openActions(page: Page) {
  await page.getByRole("button", { name: "Actions" }).click();
}

async function scheduleAndHold(page: Page) {
  // the approved cluster's Schedule send opens the confirm dialog with
  // the datetime inside it — the input defaults to tomorrow (KLOX-80)
  await openActions(page);
  await page.getByRole("button", { name: "Schedule send" }).click();
  const schedDialog = page.getByRole("dialog");
  await expect(schedDialog.getByLabel("Send at")).toBeVisible();
  await expect(schedDialog.getByText(/reader.*paying/)).toBeVisible();
  await schedDialog.getByRole("button", { name: "Schedule for later" }).click();
  // the scheduled issue locks; the header chip shows the queued time
  const scheduled = page.getByRole("button", { name: /Scheduled/ });
  await expect(scheduled).toBeVisible({ timeout: 15000 });
  // Hold unlocks back to draft — re-approval is required before sending.
  // Gate on the hold response itself: the re-approve POST races the hold
  // on a remote DB and reads status=scheduled → read_only 409.
  await scheduled.click();
  const held = page.waitForResponse(
    (r) => r.url().endsWith("/hold") && r.status() === 200,
  );
  await page.getByRole("button", { name: "Hold" }).click();
  await held;
  await expect(page.getByRole("button", { name: "Approve issue" }).first()).toBeVisible({ timeout: 15000 });
}

async function sendTestKeepsEditable(page: Page) {
  // KLOX-53: "Send a test" must not lock the issue — the send records
  // to the outbox, stamps test_sent_at and leaves the issue approved,
  // editable and schedulable. The after-sending surface (reader page
  // link, test marker) still appears. Runs before the claim, so in
  // fake-live mode this exercises the unclaimed path that once flipped
  // the issue to sent.
  const ws = await workspaceInfo(page);
  neverRealMail(ws);
  // the Revisar step carries the header action cluster (KLOX-80): the
  // primary send button and the Actions menu live there (KLOX-101)
  const stepUrl = new URL(page.url());
  stepUrl.searchParams.set("step", "revisar");
  await page.goto(stepUrl.pathname + stepUrl.search);
  // the Actions menu holds the export formats — HTML and Markdown are
  // downloads, PDF prints through the browser's Save-as-PDF (KLOX-101)
  await openActions(page);
  await expect(page.getByRole("link", { name: "HTML", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Markdown", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "PDF", exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  const [resp] = await Promise.all([
    page.waitForResponse(
      (r) => r.url().endsWith("/send") && r.request().method() === "POST",
    ),
    (async () => {
      await openActions(page);
      await page.getByRole("button", { name: "Send a test" }).click();
    })(),
  ]);
  const body = await resp.json();
  expect(body.audience).toBe("test");
  expect(body.issue.status).toBe("approved");
  expect(body.issue.test_sent_at).toBeTruthy();
  // a test send published the reader page — the menu's Preview jump
  // is a real link now, and the after-sending surface carries the same
  // destination plus the test marker (no `sent` state)
  await openActions(page);
  await expect(page.getByRole("link", { name: "Preview", exact: true })).toBeVisible({ timeout: 15000 });
  await page.keyboard.press("Escape");
  await expect(page.getByRole("link", { name: /reader page/ })).toBeVisible();
  await expect(page.getByText(/Test send recorded/)).toBeVisible();
  // still editable and still sendable — the lock never engaged. The
  // cluster's Edit button steps back to the Write step where the
  // composer lives post-approval (KLOX-80)
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  await expect(page.locator(".ProseMirror").first()).toHaveAttribute("contenteditable", "true");
  await expect(page.getByRole("button", { name: "Send", exact: true })).toBeVisible();
}

async function sendToReaders(page: Page) {
  // the send action is its own header button post-approval; readers get
  // a confirmation dialog (count, subject, sender) — never one-click
  const ws = await workspaceInfo(page);
  neverRealMail(ws);
  const synthetic = ws.mail === "live" && ws.mail_transport === "synthetic";
  const readersDialog = async () => {
    await page.getByRole("button", { name: "Send", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText(/reader.*paying/)).toBeVisible();
    await expect(dialog.getByText(/Subject|Assunto/)).toBeVisible();
    return dialog;
  };
  const sendPost = () =>
    page.waitForResponse(
      (r) => r.url().endsWith("/send") && r.request().method() === "POST",
    );
  const firstDialog = await readersDialog();
  if (synthetic) {
    // Live rules with zero confirmed readers is an intended terminal
    // state (D33): delivery "none" — nothing leaves the app, the issue
    // stays approved, the response carries a warning and the API text
    // lands as a toast — the after-sending surface must not render.
    const [noneResp] = await Promise.all([
      sendPost(),
      firstDialog.getByRole("button", { name: "Send now" }).click(),
    ]);
    const noneBody = await noneResp.json();
    expect(noneBody.delivery).toBe("none");
    expect(noneBody.issue.status).toBe("approved");
    expect(Array.isArray(noneBody.warnings) && noneBody.warnings.length > 0).toBe(true);
    await expect(page.getByText(/nothing was delivered/i).first()).toBeVisible({ timeout: 15000 });
    // A "none" delivery must not mint a sent surface: the only
    // after-sending chrome allowed is the earlier test send's marker —
    // sendTestKeepsEditable already stamped test_sent_at, so the
    // reader-page link it unlocked keeps rendering here.
    await expect(page.getByText(/Test send recorded/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Send", exact: true })).toBeVisible();
    // A test send goes to the claimed owner — and confirms them as a
    // reader, so the resend has a deliverable recipient. Success means
    // a real synthetic-provider receipt, never a fallback or a mix.
    const before = (await fakeRequests(page)).count;
    const [testResp] = await Promise.all([
      sendPost(),
      (async () => {
        await openActions(page);
        await page.getByRole("button", { name: "Send a test" }).click();
      })(),
    ]);
    const testBody = await testResp.json();
    expect(testBody.delivery).toBe("resend");
    expect(testBody.sent).toBe(1);
    expect(testBody.to).toEqual(["smoke@kloxus.dev"]);
    expect(testBody.issue.status).toBe("approved");
    await expect(page.getByText(/^Test sent to /).first()).toBeVisible({ timeout: 15000 });
    const afterTest = await fakeRequests(page);
    expect(afterTest.count - before).toBe(1);
    const testReq = afterTest.requests.at(-1)!;
    expect(testReq.to).toEqual(["smoke@kloxus.dev"]);
    expect(testReq.auth).toBe("Bearer re_local_verify_synthetic");
    const again = await readersDialog();
    await expect(again.getByText(/1 reader/)).toBeVisible();
    const [readersResp] = await Promise.all([
      sendPost(),
      again.getByRole("button", { name: "Send now" }).click(),
    ]);
    const readersBody = await readersResp.json();
    expect(readersBody.delivery).toBe("resend");
    expect(readersBody.sent).toBe(1);
    // the delivery ledger records the send as provider-sent
    const mail = await (
      await page.request.get(`/api/v1/outbox/${readersBody.outbox_email_id}`)
    ).json();
    expect(mail.email.provider).toBe("resend");
    expect(mail.email.status).toBe("sent");
    expect(mail.email.provider_id).toMatch(/^re_local_/);
    const afterAll = await fakeRequests(page);
    expect(afterAll.count - afterTest.count).toBe(1);
    expect(afterAll.requests.at(-1)!.to).toEqual(["smoke@kloxus.dev"]);
  } else {
    // outbox transport: the send records to the ledger and the issue
    // turns sent.
    await firstDialog.getByRole("button", { name: "Send now" }).click();
  }
  // the after-sending surface replaces the editor chrome
  await expect(page.getByRole("link", { name: /reader page/ })).toBeVisible({ timeout: 15000 });
  await expect(page.getByText(/guide learned|guide/i).first()).toBeVisible();

  // exports ship under both spellings — ?fmt= is the documented one,
  // ?format= is what the audit reached for (KLOX-56)
  const htmlHref = await page.getByRole("link", { name: "HTML", exact: true }).getAttribute("href");
  if (htmlHref) {
    for (const href of [htmlHref, htmlHref.replace("fmt=html", "format=html")]) {
      const resp = await page.request.get(href);
      expect(resp.status(), `export ${href}`).toBe(200);
      expect(resp.headers()["content-type"]).toContain("text/html");
      const html = await resp.text();
      expect(html).toContain("<h1>");
      // the pt-BR reader sees localized chrome, never the canonical
      // English names the body stores for fixture compatibility
      // (KLOX-56); the deterministic run must not claim AI assistance
      expect(html).toContain("O destaque");
      expect(html).not.toContain("The big one");
      expect(html).not.toContain("auxílio de IA");
      expect(html).not.toContain("AI assistance");
    }
  }
}

async function readerJourney(page: Page) {
  // the after-sending view links to the public issue page — open it and
  // confirm the non-subscriber gets the gated cut
  const href = await page.getByRole("link", { name: /reader page/ }).getAttribute("href");
  expect(href).toContain("/p/futuro-proximo/");
  await page.goto(href!.replace(/^https?:\/\/[^/]+/, ""));
  await expect(page.getByText("For paying readers")).toBeVisible({ timeout: 15000 });
  // paid checkout (KLOX-13): the label must say test/simulated before
  // any checkout opens — no real charge may ever look possible
  await expect(
    page.getByText(/test mode|modo de teste|simulated payment|pagamento de demonstração/i),
  ).toBeVisible();
  await page
    .getByLabel(/email for the access link|e-mail para o link de acesso/i)
    .fill("reader@example.com");
  await page.getByRole("button", { name: /Subscribe with PIX|Assinar com PIX/ }).click();
  // fake provider (no key configured): the "checkout" is the app's own
  // return URL — status polls confirm and re-serve the access link.
  // A live AbacatePay key instead navigates to the hosted checkout,
  // which e2e cannot settle — off-origin is a valid outcome then.
  const outcome = await Promise.race([
    page
      .waitForSelector("text=/Payment confirmed|Pagamento confirmado/i", { timeout: 30000 })
      .then(() => "paid" as const)
      .catch(() => null),
    page
      .waitForURL((u) => u.hostname.includes("abacatepay"), { timeout: 30000 })
      .then(() => "external" as const)
      .catch(() => null),
  ]);
  if (!outcome) {
    throw new Error("paid checkout neither confirmed nor redirected to AbacatePay");
  }
  if (outcome === "paid") {
    // outbox mode re-serves the magic link — opening it must unlock
    const open = page.getByRole("link", {
      name: /Open my subscriber access|Abrir meu acesso de assinante/,
    });
    if (await open.count()) {
      await open.first().click();
      await expect(page.getByText("For paying readers")).toBeHidden({ timeout: 15000 });
    }
  }
}

const viewports = [
  { name: "desktop", width: 1360, height: 850 },
  { name: "phone", width: 375, height: 740 },
];

// Transport preflight first: positive server-side evidence decides
// whether this suite may run — a live or unknown transport fails here,
// before the test body performs a single mail- or model-capable call.
test.beforeEach(async ({ page }) => {
  await preflight(page);
});

for (const vp of viewports) {
  test(`demo journey at ${vp.name} (${vp.width}px)`, async ({ page }) => {
    const isPhone = vp.width < 768;
    await page.setViewportSize({ width: vp.width, height: vp.height });
    await onboard(page);
    await boardReady(page, isPhone);

    await boardOps(page, isPhone);

    await clearFlags(page);
    await typeCommentary(page);

    // reload — commentary must persist
    const typed = "My reading of this section, for the record.";
    await page.reload();
    await expect(page.locator("[data-commentary]").getByText(typed).first()).toBeVisible({
      timeout: 30000,
    });

    await approveIssue(page);
    // a test send first: the issue must stay editable, approvable and
    // schedulable (KLOX-53) — the schedule+hold below proves the last
    await sendTestKeepsEditable(page);
    await scheduleAndHold(page);
    await approveIssue(page);
    await claimIfSynthetic(page);
    await sendToReaders(page);
    await readerJourney(page);
  });
}

test("demo reset returns to onboarding and the sample journey still works", async ({ page }) => {
  await page.setViewportSize({ width: 1360, height: 850 });
  await onboard(page);
  await boardReady(page, false);

  // a second prepare must answer on the board, not fail silently. The
  // fixture always exhausts to "Nothing new"; on the real app a feed
  // poll may have brought fresh items since the first issue — then a
  // live collection run is the equally correct outcome.
  await page.goto("/");
  await page.getByRole("link", { name: "Dashboard" }).click();
  // the sidebar repeats the single newsletter's link (KLOX-66) — the
  // publication card is the one inside main
  await page.getByRole("main").getByRole("link", { name: /Futuro Próximo/ }).click();
  await page.getByRole("button", { name: "Prepare next issue" }).click();
  await expect(
    page
      .getByText(/Nothing new/)
      .or(page.getByText(/Preparing the issue/))
      .or(page.getByRole("heading", { name: "This issue" })),
  ).toBeVisible({ timeout: process.env.E2E_BASE_URL ? 840_000 : 600_000 });

  // reset wipes the workspace — the visitor walks onboarding again and
  // the fixture sources collect a fresh issue. The reset affordance is
  // demo-only: on the real app it does not render, and the second-
  // prepare check above already proved the hosted path.
  await page.goto("/publications");
  const reset = page.getByRole("button", { name: "Reset test data" });
  if (!(await reset.isVisible())) return;
  await reset.click();
  await expect(page.getByRole("button", { name: /Create your newsletter/ }).first()).toBeVisible({
    timeout: 15000,
  });
  await onboard(page);
  await boardReady(page, false);
});
