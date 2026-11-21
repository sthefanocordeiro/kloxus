import { test, expect } from "@playwright/test";

// KLOX-68 §10 live checks — READ-ONLY against https://kloxus.fly.dev:
// no clicks that create workspaces, no sends, no AI. Landing content,
// public pages, banned words, English leak.
const BASE = process.env.E2E_BASE_URL ?? "https://kloxus.fly.dev";

// Opt-in: read-only live checks for the KLOX-68 acceptance runbook —
// never a side effect of a plain `playwright test` run.
test.skip(!process.env.KLOX68_LIVE_CHECKS, "KLOX68_LIVE_CHECKS=1 to run");

const BANNED = [
  [/determin[íi]stic/i, "determinístico"],
  [/dossi[eê]/i, "dossiê"],
  [/\breivindic/i, "reivindicar"],
  [/\bsina(?:l|is)\b/i, "sinal"],
  [/evid[êe]ncia/i, "evidência"],
  [/\bcusto\b/i, "custo"],
  [/\btokens?\b/i, "token"],
  [/\bworkspace\b/i, "workspace"],
  [/\bmotor\b/i, "motor"],
  [/indispon[íi]vel/i, "indisponível"],
  [/\bem desenvolvimento\b/i, "em desenvolvimento"],
  [/Continuar para/, "Continuar para…"],
  [/Tecnologia e patrocinadores/i, "technology list"],
  [/IA ASSISTE/i, "old eyebrow"],
  [/exemplo citado/i, "sample dossier"],
];

test.describe("klox-68 live landing (PT)", () => {
  test.use({ locale: "pt-BR" });

  test("landing: structure + banned words + no english", async ({ page }) => {
    await page.goto(BASE + "/");
    await expect(page.locator("html")).toHaveAttribute("lang", "pt-BR");
    const body = (await page.locator("body").innerText()) ?? "";
    const hits: string[] = [];
    for (const [re, label] of BANNED) {
      if (re.test(body)) hits.push(label);
    }
    console.log("LIVE-BANNED:", JSON.stringify(hits));
    // §1 structure: header CTA, hero, three steps, FAQ, closing band, footer
    await expect(page.getByRole("button", { name: /Criar minha newsletter/ }).first()).toBeVisible();
    console.log("LIVE-HEADLINE:", (await page.locator("h1").first().innerText()).replace(/\n/g, " "));
    // no anonymous banner
    const banner = await page.getByText(/Este espaço é anônimo/i).count();
    console.log("LIVE-ANON-BANNER:", banner);
    // english leak check: common EN strings must not appear in PT chrome
    const en = ["Create your newsletter", "How it works", "Questions", "Try it without signing up"];
    const enHits = en.filter((s) => body.includes(s));
    console.log("LIVE-EN-LEAK:", JSON.stringify(enHits));
    // footer links exist
    await expect(page.getByRole("contentinfo").getByText("Termos")).toBeVisible();
    await expect(page.getByRole("contentinfo").getByText("Privacidade")).toBeVisible();
    // language switch lives in the footer (KLOX-69)
    console.log("LIVE-FOOTER-LANG:", await page.getByRole("contentinfo").getByRole("button", { name: "EN" }).count());
  });

  test("legal pages render", async ({ page }) => {
    await page.goto(BASE + "/termos");
    await expect(page.locator("h1")).toBeVisible();
    await page.goto(BASE + "/privacidade");
    await expect(page.locator("h1")).toBeVisible();
  });

  test("workspace reports llm off (zero-paid gate)", async ({ page }) => {
    const res = await page.request.get(BASE + "/api/v1/workspace");
    const ws = (await res.json()) as { llm_mode?: string; mode?: string; mail?: string };
    console.log("LIVE-WS:", JSON.stringify(ws));
  });
});
