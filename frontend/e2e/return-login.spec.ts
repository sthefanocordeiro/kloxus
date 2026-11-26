import { expect, test } from "@playwright/test";
import { onboard } from "./onboard.ts";
import { fakeLive, fakeRequests } from "./mail.ts";

// KLOX-272 regression: the central returning-user journey — create a
// Radar, sign out, come back through "Already have an account? Sign in"
// with the same email, and land on the same Radar rather than an empty
// replacement workspace.
test("logout/login with the same email recovers the previous Radar", async ({ page }) => {
  const email = await onboard(page);
  const pubs = await (await page.request.get("/api/v1/publications")).json();
  const pubID = pubs.publications[0].id as number;
  const radarName = pubs.publications[0].name as string;

  // Sign out through the account menu, as the user does.
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.waitForURL((u) => u.pathname === "/");

  // The return path is explicit email recovery — never the create CTA.
  await page.getByRole("button", { name: "Already have an account? Sign in" }).click();
  await page.waitForURL(/\/entrar/);
  await expect(page.getByRole("heading", { name: "Sign back in" })).toBeVisible();
  await page.getByLabel("Email").fill(email);
  const fakeCount = fakeLive ? (await fakeRequests(page)).count : 0;
  const loginResponse = page.waitForResponse(
    (r) => r.url().includes("/api/v1/auth/login") && r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Send sign-in link" }).click();
  await expect(page.getByText(/we sent a sign-in link/i)).toBeVisible();

  // This step is the user opening the email. Outbox mode exposes the
  // link on the API response for the test inbox; fake-live mode proves
  // it crossed the synthetic provider and reads the received body.
  const body = await (await loginResponse).json();
  let link = body.login_url as string | undefined;
  if (!link && fakeLive) {
    for (let i = 0; i < 20; i++) {
      const reqs = await fakeRequests(page);
      const received = reqs.requests.slice(fakeCount).at(-1)?.text;
      link = received?.match(/https?:\/\/[^\s)]+\/login\?[^\s)]+/)?.[0];
      if (link) break;
      await page.waitForTimeout(250);
    }
  }
  expect(link, "sign-in email link").toBeTruthy();
  await page.goto(link!);
  await expect(page.getByRole("button", { name: "Sign in and continue" })).toBeVisible();
  await page.getByRole("button", { name: "Sign in and continue" }).click();

  await page.waitForURL(/\/publications/);
  await expect(page.getByRole("main").getByRole("link", { name: radarName })).toBeVisible();
  await expect(page.getByRole("button", { name: /Create your Radar/ })).toHaveCount(0);

  await page.goto(`/publications/${pubID}/today`);
  await expect(page.getByRole("heading", { name: "Today's stories" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText(/doesn't exist or belongs to another account/i)).toHaveCount(0);
});
