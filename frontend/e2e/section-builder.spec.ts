import { expect, test } from "@playwright/test";

// KLOX-65: the creator-defined section builder and the Configurações
// screen. This spec stops before the first collection — creating the
// publication is enough to reach Seções and Settings, so the journey
// stays fast in every mode. Deleting the newsletter cleans the
// workspace back up at the end.
test("section builder onboarding, Seções screen and Configurações delete", async ({
  page,
}) => {
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: /The research is done/ }),
  ).toBeVisible({ timeout: 15000 });
  await page
    .getByRole("button", { name: /Create your Radar/ })
    .first()
    .click();
  await page.waitForURL(/\/new/);

  // step 1: the proposal pre-fills the blocks
  await expect(page.locator('input[value*="Futuro"]')).toBeVisible({
    timeout: 15000,
  });
  await page.getByRole("button", { name: "Next", exact: true }).click();

  // step 2: the builder lists the proposal's sections; adding a section
  // moves the live edition total
  await expect(
    page.getByText(/Your piece will have \d+ stories/),
  ).toBeVisible();
  await page.getByRole("button", { name: "Add section" }).click();
  await expect(
    page.getByText(/Your piece will have \d+ stories/),
  ).toBeVisible();
  const newName = page.getByPlaceholder("Section name").last();
  await newName.fill("Quick links");
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.waitForURL(/\/publications\/(\d+)\/sources/);
  const pubId = page.url().match(/publications\/(\d+)/)![1];

  // Seções: the same builder edits the saved list; the new section is there.
  // Wait for the nav target to resolve to this publication before clicking —
  // on hosted latency a click can land before the shell's links are pub-
  // scoped, and /publications/<word> is an unrecoverable dead route (KLOX-74).
  const sectionsNav = page.getByRole("link", { name: "Sections", exact: true }).first();
  await expect(sectionsNav).toHaveAttribute("href", `/publications/${pubId}/sections`);
  await sectionsNav.click();
  await page.waitForURL(new RegExp(`/publications/${pubId}/sections`));
  const added = page.getByPlaceholder("Section name").last();
  await expect(added).toHaveValue("Quick links");
  // renaming keeps the edition structure intact — save persists
  await added.fill("Links I liked");
  await page.getByRole("button", { name: "Save sections", exact: true }).click();
  await expect(
    page.getByText(/Sections saved — they apply to the next edition/),
  ).toBeVisible({ timeout: 15000 });

  // Configurações: editable identity fields plus the danger zone
  const settingsNav = page.getByRole("link", { name: "Settings", exact: true }).first();
  await expect(settingsNav).toHaveAttribute("href", `/publications/${pubId}/settings`);
  await settingsNav.click();
  await page.waitForURL(new RegExp(`/publications/${pubId}/settings`));
  const nameField = page.getByLabel(/^Name$/);
  await expect(nameField).toBeVisible();
  await nameField.fill("Futuro em Pauta — edição teste");
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  await expect(page.getByText("Settings saved.")).toBeVisible({ timeout: 15000 });
  await expect(page.getByRole("link", { name: "Editorial guide" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Outbox" })).toBeVisible();

  // danger zone: deletion sits behind an explicit confirmation dialog —
  // cancelling keeps the newsletter, confirming removes it
  await page.getByRole("button", { name: "Delete Radar", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByRole("heading", { name: "Delete this Radar?" }),
  ).toBeVisible();
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(dialog).toBeHidden();
  await page.getByRole("button", { name: "Delete Radar", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Yes, delete it", exact: true })
    .click();
  await page.waitForURL(/\/publications/);
});
