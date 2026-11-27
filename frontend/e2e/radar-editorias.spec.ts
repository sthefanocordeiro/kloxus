import { expect, test } from "@playwright/test";
import { onboard } from "./onboard.ts";

// KLOX-274: the hosted-smoke regression — agronegócio was typed as a
// theme and picked as an editoria. On the Radar screen the editoria is
// the selected chip, while the theme field keeps the creator's other
// topics without making the absorbed label look like lost data.
test("creation with Agronegócio reaches the Radar screen as a selected editoria", async ({ page }) => {
  await onboard(page, undefined, {
    name: "Agro no campo",
    topics: "agronegócio, pecuária, clima no campo, commodities agrícolas",
    nicheLabels: ["Agribusiness"],
  });

  const pubs = await (await page.request.get("/api/v1/publications")).json();
  const pubID = pubs.publications[0].id as number;
  await page.goto(`/publications/${pubID}/sources`);

  await expect(page.getByLabel("Topics")).toHaveValue(
    "pecuária, clima no campo, commodities agrícolas",
  );
  await expect(page.getByRole("button", { name: "Agribusiness", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await expect(page.getByText(/stay here as chips/i)).toBeVisible();
});
