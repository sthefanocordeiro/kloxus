import { expect, test, type Page } from "@playwright/test";

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
const benchCol = (page: Page) => page.locator('section[aria-label="Pautas"]');
const sectionBox = (page: Page, name: string) =>
  issueCol(page).locator(`[data-section="${name}"]`);

// The workspace starts empty — every journey walks onboarding first:
// landing → /new → /pub/:id/sources → confirm → first run → board.
async function onboard(page: Page) {
  await page.goto("/");
  // the landing renders the cited sample dossier before any workspace
  await expect(page.getByText(/dossier from the sample issue/i)).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: /Get started without login/ }).first().click();
  await page.waitForURL(/\/new/);

  // the sample proposal pre-fills the five editable blocks
  await expect(page.locator('input[value*="Futuro"]')).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "Create newsletter", exact: true }).click();
  await page.waitForURL(/\/publications\/\d+\/sources/);

  // the sources screen offers the fixture sources — preview one, then
  // confirm all: the first collection runs automatically into the board
  await expect(page.getByText("Suggested sources")).toBeVisible();
  await page.getByRole("button", { name: "Latest items", exact: true }).first().click();
  await expect(page.locator("ul li a").first()).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: /Confirm \d+ sources & collect/ }).click();
  // the first real-mode run collects, dedupes and triages live feeds —
  // minutes, not the fixture's seconds
  await page.waitForURL(/\/issues\/\d+/, { timeout: 600000 });
}

async function boardReady(page: Page, isPhone: boolean) {
  // the step rail finishes, then the board lands. On phone the issue
  // column sits behind its tab — the tab bar is the ready signal there.
  await expect(
    isPhone
      ? page.getByRole("button", { name: /This issue/ })
      : page.getByRole("heading", { name: "This issue" }),
  ).toBeVisible({ timeout: 120000 });
  await expect(benchCol(page).locator("p.font-doc").first()).toBeVisible({ timeout: 60000 });
}

async function boardOps(page: Page, isPhone: boolean) {
  const tabIssue = page.getByRole("button", { name: /This issue/ });
  const tabBench = page.getByRole("button", { name: /^Pautas/ });
  if (isPhone) await tabIssue.click();

  // 1) remove a Worth-knowing story giving a reason — the guide learns
  const wk = sectionBox(page, "Worth knowing");
  const removedTitle = (await wk.locator("p.font-doc").first().textContent())!.trim();
  await wk.getByRole("button", { name: "Remove", exact: true }).first().click();
  await page.getByRole("button", { name: "not for my audience" }).click();
  await expect(page.getByText(/Rule learned/)).toBeVisible({ timeout: 15000 });

  // 2) add a story from the bench with the "+" button (nth(1): the just-
  //    removed story re-ranks first on the bench — pick a different one
  //    so the persistence check below stays meaningful). A full suggested
  //    section opens the section chooser instead of failing.
  if (isPhone) await tabBench.click();
  await benchCol(page).getByRole("button", { name: "Add to issue", exact: true }).nth(1).click();
  const chooser = page.getByRole("dialog");
  await page.waitForTimeout(500);
  if (await chooser.isVisible()) {
    await chooser.getByRole("button", { name: /Quick links/ }).click();
  }
  await expect(page.getByText(/Added/)).toBeVisible({ timeout: 15000 });

  // 3) move a Worth-knowing story into Quick links via the move menu
  if (isPhone) await tabIssue.click();
  await sectionBox(page, "Worth knowing")
    .getByRole("button", { name: /Move to/ })
    .first()
    .click();
  await page.getByRole("button", { name: /Quick links/ }).click();
  await expect(page.getByText(/Moved/)).toBeVisible({ timeout: 15000 });

  // 4) the lineup persists across a reload — the rejected story stays
  //    benched instead of snapping back into its section
  await page.reload();
  await boardReady(page, isPhone);
  await expect(benchCol(page).getByText(removedTitle)).toBeVisible({ timeout: 30000 });
  if (isPhone) await tabIssue.click();

  // 5) draft — the step rail runs, then the editor lands
  await page.getByRole("button", { name: /Draft the issue/ }).click();
  await expect(page.locator("[data-sid]").first()).toBeVisible({ timeout: 600000 });

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
  for (let i = 0; i < 40; i++) {
    if ((await remove.count()) === 0) break;
    try {
      await remove.first().click({ timeout: 10_000 });
    } catch {
      // the node detached mid-click — re-poll
    }
    await page.waitForTimeout(800);
  }
  // every Remove — disabled zombie node views included — must be gone so
  // the last removal has settled and the editor rebuilt before the next
  // step; a pending DELETE would race the first commentary save (409).
  await expect(page.getByRole("button", { name: "Remove" })).toHaveCount(0, { timeout: 30_000 });
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

async function scheduleAndHold(page: Page) {
  // post-approval the send popover schedules with a real date+time —
  // the datetime input defaults to tomorrow
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await expect(page.getByLabel("Send at")).toBeVisible();
  await page.getByRole("button", { name: "Schedule for later" }).click();
  // scheduling confirms through the same dialog as sending — count, subject, sender
  const schedDialog = page.getByRole("dialog");
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

async function sendToReaders(page: Page) {
  // the send action is its own header button post-approval; readers get
  // a confirmation dialog (count, subject, sender) — never one-click
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await page.getByRole("button", { name: "Send to readers" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText(/reader.*paying/)).toBeVisible();
  await expect(dialog.getByText(/Subject|Assunto/)).toBeVisible();
  await dialog.getByRole("button", { name: "Send now" }).click();
  // the after-sending surface replaces the editor chrome
  await expect(page.getByRole("link", { name: /reader page/ })).toBeVisible({ timeout: 15000 });
  await expect(page.getByText(/guide learned|guide/i).first()).toBeVisible();
}

async function readerJourney(page: Page) {
  // the after-sending view links to the public issue page — open it and
  // confirm the non-subscriber gets the gated cut
  const href = await page.getByRole("link", { name: /reader page/ }).getAttribute("href");
  expect(href).toContain("/p/futuro-proximo/");
  await page.goto(href!.replace(/^https?:\/\/[^/]+/, ""));
  await expect(page.getByText("For paying readers")).toBeVisible({ timeout: 15000 });
  // subscribe → the magic-link offer appears
  await page.getByRole("button", { name: "Subscribe" }).click();
  await expect(page.getByText(/magic link/i).first()).toBeVisible({ timeout: 15000 });
}

const viewports = [
  { name: "desktop", width: 1360, height: 850 },
  { name: "phone", width: 375, height: 740 },
];

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
    await scheduleAndHold(page);
    await approveIssue(page);
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
  await page.getByRole("link", { name: /Continue to/ }).click();
  await page.getByRole("button", { name: "Prepare next issue" }).click();
  await expect(
    page
      .getByText(/Nothing new/)
      .or(page.getByText(/Preparing the issue/))
      .or(page.getByRole("heading", { name: "This issue" })),
  ).toBeVisible({ timeout: 600000 });

  // reset wipes the workspace — the visitor walks onboarding again and
  // the fixture sources collect a fresh issue
  await page.goto("/publications");
  await page.getByRole("button", { name: "Reset demo" }).click();
  await expect(page.getByRole("button", { name: /Get started without login/ }).first()).toBeVisible({
    timeout: 15000,
  });
  await onboard(page);
  await boardReady(page, false);
});
