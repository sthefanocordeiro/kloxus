import { expect, test } from "@playwright/test";
import { onboard } from "./onboard";

// KLOX-122: with KLOXUS_PUBLISHING off (the shipped default) the app is
// an editor de pauta — no send, readers, paywall or public pages. Local
// runs need E2E_PUBLISHING=off; a hosted target already runs that way.
test.skip(
  process.env.E2E_PUBLISHING !== "off" && !process.env.E2E_BASE_URL,
  "needs the publishing flag off (E2E_PUBLISHING=off)",
);

test("publishing off: Hoje · Pautas · Radar · Configurações, no send/readers copy, /p 404", async ({ page }) => {
  await onboard(page);

  // sidebar is exactly the four entries
  const side = page.getByRole("navigation", { name: "Radar" }).first();
  await expect(side.getByRole("link")).toHaveText(["Today", "Briefs", "Radar", "Settings"]);

  // reader-facing routes are gone from the server
  for (const path of ["/p/radar", "/p/radar/1", "/unsub", "/confirm-sub"]) {
    const res = await page.request.get(path);
    expect(res.status(), path).toBe(404);
  }

  // no screen the creator can reach mentions readers, sending or price
  const banned = /\b(readers?|subscribers?|paywall|outbox|send test|schedule send|price)\b/i;
  for (const name of ["Briefs", "Radar", "Settings"]) {
    await side.getByRole("link", { name, exact: true }).click();
    await expect(page.locator("main")).not.toContainText(banned);
    await expect(page.locator("aside")).not.toContainText(banned);
  }
});
