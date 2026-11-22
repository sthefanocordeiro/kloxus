import { expect, test } from "@playwright/test";
import { PUBLISHING_SKIP_REASON, publishingOff } from "./publishing.ts";

test.skip(publishingOff, PUBLISHING_SKIP_REASON);

// KLOX-72: the newsletter template editor under Configurações —
// reachable from the settings link card, seeded with one story slot
// per section until the first save, fixed blocks editable inline.
// Stops before any collection: template GET/PUT is enough, so the
// journey stays fast in every mode. Deletes the newsletter at the end
// to leave the workspace clean.
test("newsletter template: defaults, edit and save", async ({ page }) => {
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: /The research is ready/ }),
  ).toBeVisible({ timeout: 15000 });
  await page
    .getByRole("button", { name: /Create your Radar/ })
    .first()
    .click();
  await page.waitForURL(/\/new/);

  await expect(page.locator('input[value*="Futuro"]')).toBeVisible({
    timeout: 15000,
  });
  await page.getByRole("button", { name: "Next", exact: true }).click();
  // both steps now share the one "Next" label — wait for the section
  // step to render so the second click can't land on page 1's button
  await expect(page.getByText(/Your piece will have \d+ stor/)).toBeVisible();
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.waitForURL(/\/publications\/(\d+)\/sources/);
  const pubId = page.url().match(/publications\/(\d+)/)![1];

  // Configurações carries the template link card
  const settingsNav = page.getByRole("link", { name: "Settings", exact: true }).first();
  await expect(settingsNav).toHaveAttribute("href", `/publications/${pubId}/settings`);
  await settingsNav.click();
  await page.waitForURL(new RegExp(`/publications/${pubId}/settings`));
  await page.getByRole("link", { name: "Radar template" }).click();
  await page.waitForURL(new RegExp(`/publications/${pubId}/template`));

  // the effective default is one story slot per section
  await expect(
    page.getByRole("heading", { name: "Radar template" }),
  ).toBeVisible({ timeout: 15000 });
  const slots = page.getByRole("combobox", { name: "Story" });
  await expect(slots).toHaveCount(3);

  // a fixed text block edits inline and saves
  await page.getByRole("button", { name: "Text", exact: true }).click();
  await page.getByPlaceholder("Write a note for readers…").fill("Bom dia — header note");
  await page.getByRole("button", { name: "Save template", exact: true }).click();
  await expect(
    page.getByText("Template saved — new pieces start from it."),
  ).toBeVisible({ timeout: 15000 });

  // reload keeps the saved list — four rows now
  await page.reload();
  await expect(
    page.getByPlaceholder("Write a note for readers…"),
  ).toHaveValue("Bom dia — header note", { timeout: 15000 });
  await expect(slots).toHaveCount(3);

  // cleanup: delete the newsletter so the workspace returns to empty
  await settingsNav.click();
  await page.waitForURL(new RegExp(`/publications/${pubId}/settings`));
  await page.getByRole("button", { name: "Delete Radar", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Yes, delete it", exact: true })
    .click();
  await page.waitForURL(/\/publications/);
});
