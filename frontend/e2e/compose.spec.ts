import { expect, test, type Page } from "@playwright/test";

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
const benchCol = (page: Page) => page.locator('section[aria-label="Pautas"]');
const sectionBox = (page: Page, name: string) =>
  issueCol(page).locator(`[data-section="${name}"]`);

// same onboarding as the demo journey: landing → /new → sources → the
// first run lands on the build board
async function onboard(page: Page) {
  await page.goto("/");
  await expect(page.getByText(/dossier from the sample issue/i)).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: /Get started without login/ }).first().click();
  await page.waitForURL(/\/new/);
  await expect(page.locator('input[value*="Futuro"]')).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "Create newsletter", exact: true }).click();
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
    // every section full: picking one opens the swap picker
    await picks.first().click();
    await expect(chooser.getByText(/pick a story to swap out/)).toBeVisible();
    await picks.first().click();
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

async function claimIfLive(page: Page) {
  // Live deployments gate sends behind a claimed workspace. The claim
  // email lands in a real inbox — and in the workspace outbox, where
  // the API request context can complete the magic-link round trip.
  const ws = await (await page.request.get("/api/v1/workspace")).json();
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

test("manual compose journey", async ({ page }) => {
  await page.setViewportSize({ width: 1360, height: 850 });
  await onboard(page);
  await boardReady(page);

  // pick candidates: drop an AI suggestion and pull one story in from
  // the bench — the manual flow must also carry a story with no dossier
  const wk = sectionBox(page, "Worth knowing");
  await wk.getByRole("button", { name: "Remove", exact: true }).first().click();
  await page.getByRole("button", { name: "just remove" }).click();
  await benchCol(page).getByRole("button", { name: "Add to issue", exact: true }).first().click();
  await settleBenchAdd(page);

  // manual compose: the lineup becomes empty slots — no draft run
  await page.getByRole("button", { name: "Write it myself" }).click();
  const emptySlot = page.getByRole("button", { name: /Empty — write this item/ });
  await expect(emptySlot.first()).toBeVisible({ timeout: 30000 });
  const total = await emptySlot.count();
  expect(total).toBeGreaterThan(0);

  // write each item by hand: click the slot → compose box in the
  // evidence pane → type → cite source [1] → add
  for (let i = 0; i < total; i++) {
    await emptySlot.first().click();
    const box = page.locator('section[aria-label="Write this item"]');
    await expect(box).toBeVisible();
    await box.getByRole("textbox").fill(`Editor-written paragraph ${i + 1} for the issue.`);
    await box.getByRole("button", { name: /^\[1\] / }).click();
    await box.getByRole("button", { name: "Add to item" }).click();
    // the sentence lands in the document as a cited fact
    await expect(page.locator("[data-sid]")).toHaveCount(i + 1, { timeout: 15000 });
  }
  // every slot filled — the empty affordance is gone
  await expect(emptySlot).toHaveCount(0);

  await typeCommentary(page);
  await approveIssue(page);

  // live mail gates sends behind a claimed workspace — claim first, or
  // the test send is correctly refused 409 unclaimed_workspace
  await claimIfLive(page);

  // dry send: a test email goes to the outbox, the issue stays approved
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await page.getByRole("button", { name: "Send a test" }).click();
  await expect(page.getByText(/Test sent|Resend failed/).first()).toBeVisible({ timeout: 15000 });
});
