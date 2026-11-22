import { expect, test, type Page } from "@playwright/test";

// KLOX-68 §10 automated checks — runs against every screen at 1440 and
// 390 in PT and reports: banned words in rendered text, touch-target
// sizes, primary-button counts, bare arrows, and WCAG AA contrast.
// Acceptance runbook (SHOT_DIR set => runs); the report prints as a
// CHECKS-JSON line after the run.
const dir = process.env.SHOT_DIR;
test.skip(!dir, "acceptance runbook — set SHOT_DIR to run");

type Finding = { screen: string; kind: string; detail: string };
const findings: Finding[] = [];

// §4.5/§2/§10 banned on any screen (rendered text, PT + EN)
const BANNED: Array<[RegExp, string]> = [
  [/determin[íi]stic/i, "determinístico"],
  [/dossi[eê]/i, "dossiê"],
  [/\breivindic/i, "reivindicar"],
  [/\bsina(?:l|is)\b/i, "sinal"],
  [/\bescala[çc]/i, "escalação"],
  [/evid[êe]ncia/i, "evidência"],
  [/\bcusto\b/i, "custo"],
  [/\bcost\b/i, "cost"],
  [/\btokens?\b/i, "token"],
  [/\bworkspace\b/i, "workspace"],
  [/\bespa[çc]o\b/i, "espaço"],
  [/\bmotor\b/i, "motor"],
  [/\bengine\b/i, "engine"],
  [/\bvers[ãa]o\s+(?:do\s+documento|c[0-9a-f])/i, "versão/hash"],
  [/\bem desenvolvimento\b/i, "em desenvolvimento"],
  [/indispon[íi]vel/i, "indisponível"],
  [/\bunavailable\b/i, "unavailable"],
  [/\bIA desligada\b/i, "IA desligada"],
  [/\bAI (?:is )?off\b/i, "AI off"],
  [/\bv\d{2,}\b/i, "version counter"],
  [/\b[0-9a-f]{8}\b\s*$/, "hash-like id"],
];

async function auditScreen(page: Page, screen: string) {
  // rendered text sweep — visible text only
  const text = await page.evaluate(() => {
    const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    let out = "";
    let n: Node | null;
    while ((n = walk.nextNode())) {
      const el = (n as Text).parentElement;
      if (!el) continue;
      const r = el.getBoundingClientRect();
      if (r.width === 0 && r.height === 0) continue;
      const s = getComputedStyle(el);
      if (s.visibility === "hidden" || s.display === "none") continue;
      out += (n as Text).data + "\n";
    }
    return out;
  });
  for (const [re, label] of BANNED) {
    const m = text.match(re);
    if (m) findings.push({ screen, kind: "banned-word", detail: `${label}: …${text.slice(Math.max(0, (m.index ?? 0) - 30), (m.index ?? 0) + 40).replace(/\n/g, " ")}…` });
  }

  // touch targets + bare arrows + primary count
  const buttons = await page.evaluate(() => {
    const out: Array<{ name: string; w: number; h: number; primary: boolean; arrow: boolean }> = [];
    for (const el of document.querySelectorAll("button, [role=button]")) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      const s = getComputedStyle(el);
      if (s.visibility === "hidden" || s.display === "none") continue;
      const name = (el.getAttribute("aria-label") ?? el.textContent ?? "").trim().slice(0, 40);
      const cls = el.className || "";
      out.push({
        name,
        w: Math.round(r.width),
        h: Math.round(r.height),
        primary: /bg-teal\b/.test(String(cls)) && /text-white/.test(String(cls)),
        arrow: /^[←→]$/.test(name),
      });
    }
    return out;
  });
  const mobile = (page.viewportSize()?.width ?? 1440) < 768;
  const minH = mobile ? 44 : 40;
  for (const b of buttons) {
    if (b.arrow) findings.push({ screen, kind: "bare-arrow", detail: b.name });
    if (b.primary) continue; // counted below
    if ((b.h < minH || b.w < minH) && b.h > 0) {
      findings.push({ screen, kind: "touch-target", detail: `"${b.name}" ${b.w}x${b.h} < ${minH}px` });
    }
  }
  const primaries = buttons.filter((b) => b.primary).length;
  if (primaries > 1) findings.push({ screen, kind: "primary-count", detail: `${primaries} primary buttons` });

  // contrast AA — every text node over a solid background. Tailwind v4
  // emits oklab() and color-mix(in oklab, …) — both parsed here, with
  // alpha blended over the nearest solid ancestor (a bare "skip" would
  // hide the hero's paper/80-on-teal subhead).
  const contrast = await page.evaluate(() => {
    const oklabToRgb = (L: number, a: number, b: number): [number, number, number] => {
      const l = Math.pow(L + 0.3963377774 * a + 0.2158037573 * b, 3);
      const m = Math.pow(L - 0.1055613458 * a - 0.0638541728 * b, 3);
      const s = Math.pow(L - 0.0894841775 * a - 1.291485548 * b, 3);
      const R = 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s;
      const G = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s;
      const B = -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s;
      const enc = (x: number) => {
        const v = Math.min(1, Math.max(0, x));
        return v <= 0.0031308 ? 12.92 * v : 1.055 * Math.pow(v, 1 / 2.4) - 0.055;
      };
      return [enc(R) * 255, enc(G) * 255, enc(B) * 255];
    };
    // returns [r,g,b] premultiplied NOT applied; alpha separate
    const parseColor = (c: string): { rgb: [number, number, number]; a: number } | null => {
      c = c.trim();
      let m = c.match(/^rgba?\(([^)]+)\)$/);
      if (m) {
        const parts = m[1].split(",").map((x) => parseFloat(x));
        return { rgb: [parts[0], parts[1], parts[2]], a: parts[3] ?? 1 };
      }
      m = c.match(/^oklab\(([-\d.]+%?)\s+([-\d.]+)\s+([-\d.]+)(?:\s*\/\s*([\d.]+%?)?)?\)$/);
      if (m) {
        const L = m[1].endsWith("%") ? parseFloat(m[1]) / 100 : parseFloat(m[1]);
        const a = parseFloat(m[2]);
        const b = parseFloat(m[3]);
        const alpha = m[4] ? (m[4].endsWith("%") ? parseFloat(m[4]) / 100 : parseFloat(m[4])) : 1;
        return { rgb: oklabToRgb(L, a, b), a: alpha };
      }
      m = c.match(/^color-mix\(in\s+oklab,\s*([^,]+),\s*([^)]+)\)$/);
      if (m) {
        const p1 = m[1].trim();
        const p2 = m[2].trim();
        const pct = (s: string) => {
          const pm = s.match(/([\d.]+)%/);
          return pm ? parseFloat(pm[1]) / 100 : null;
        };
        const isTransparent = (s: string) => /^transparent$/i.test(s);
        const a1 = pct(p1);
        const a2 = pct(p2);
        if (isTransparent(p1) && a2 !== null) {
          const base = parseColor(p2.replace(/ \d+(\.\d+)?%$/, ""));
          return base ? { rgb: base.rgb, a: base.a * a2 } : null;
        }
        if (isTransparent(p2) && a1 !== null) {
          const base = parseColor(p1.replace(/ \d+(\.\d+)?%$/, ""));
          return base ? { rgb: base.rgb, a: base.a * a1 } : null;
        }
        // two real colors — average (rare in this UI)
        const c1 = parseColor(p1);
        const c2 = parseColor(p2);
        if (c1 && c2) {
          const w1 = a1 ?? 0.5;
          const w2 = a2 ?? 0.5;
          return {
            rgb: [0, 1, 2].map((i) => (c1.rgb[i] * w1 + c2.rgb[i] * w2) / (w1 + w2)) as [number, number, number],
            a: 1,
          };
        }
        return null;
      }
      if (/^(?:transparent|none)$/.test(c)) return { rgb: [0, 0, 0], a: 0 };
      return null;
    };
    const lum = (rgb: [number, number, number]) => {
      const f = (v: number) => {
        const x = v / 255;
        return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
      };
      return 0.2126 * f(rgb[0]) + 0.7152 * f(rgb[1]) + 0.0722 * f(rgb[2]);
    };
    const ratio = (fg: [number, number, number], bg: [number, number, number]) => {
      const l1 = lum(fg);
      const l2 = lum(bg);
      return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
    };
    // effective solid background under el, blending translucent layers
    const bgOf = (el: Element): [number, number, number] | null => {
      let acc: [number, number, number] | null = null;
      let cur: Element | null = el;
      while (cur) {
        const parsed = parseColor(getComputedStyle(cur).backgroundColor);
        if (parsed && parsed.a > 0) {
          const blended: [number, number, number] = acc
            ? ([0, 1, 2].map((i) => parsed.rgb[i] * parsed.a + acc[i] * (1 - parsed.a)) as [number, number, number])
            : parsed.rgb;
          if (parsed.a >= 0.99) return blended;
          acc = blended;
        }
        cur = cur.parentElement;
      }
      return acc;
    };
    const bad: string[] = [];
    const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    let n: Node | null;
    const seen = new Set<string>();
    while ((n = walk.nextNode())) {
      const t = (n as Text).data.trim();
      if (!t) continue;
      const el = (n as Text).parentElement;
      if (!el) continue;
      // decorative text (aria-hidden) is exempt from contrast (WCAG 1.4.3)
      if (el.closest("[aria-hidden='true']")) continue;
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      const s = getComputedStyle(el);
      if (s.visibility === "hidden" || s.display === "none" || parseFloat(s.opacity) < 0.9) continue;
      const bg = bgOf(el);
      if (!bg) continue; // over image/gradient — skipped
      const fgParsed = parseColor(s.color);
      if (!fgParsed) continue;
      // blend translucent foreground over the background
      const fg: [number, number, number] = fgParsed.a >= 0.99
        ? fgParsed.rgb
        : ([0, 1, 2].map((i) => fgParsed.rgb[i] * fgParsed.a + bg[i] * (1 - fgParsed.a)) as [number, number, number]);
      const key = `${s.color}|${bg.join(",")}|${s.fontSize}|${s.fontWeight}`;
      if (seen.has(key)) continue;
      seen.add(key);
      const cr = ratio(fg, bg);
      const size = parseFloat(s.fontSize);
      const bold = parseInt(s.fontWeight) >= 700;
      const large = size >= 24 || (size >= 18.66 && bold);
      const need = large ? 3 : 4.5;
      if (cr < need) bad.push(`"${t.slice(0, 30)}" ${s.color} on bg(${bg.join(",")}) = ${cr.toFixed(2)} (need ${need})`);
    }
    return bad;
  });
  for (const c of contrast) findings.push({ screen, kind: "contrast", detail: c });
}

async function onboardToBoard(page: Page): Promise<string> {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: /A pauta pronta/ })).toBeVisible({ timeout: 15000 });
  await auditScreen(page, "landing");
  await page.getByRole("button", { name: /Criar meu Radar/ }).first().click();
  await page.waitForURL(/\/new/);
  await expect(page.locator('input[value*="Futuro"]').first()).toBeVisible({ timeout: 15000 });
  await auditScreen(page, "onboard-step1");
  await page.getByRole("button", { name: "Avançar", exact: true }).click();
  await expect(page.getByText(/Sua peça terá \d+ pautas/)).toBeVisible();
  await auditScreen(page, "onboard-step2-sections");
  await page.getByRole("button", { name: "Avançar", exact: true }).click();
  await page.waitForURL(/\/publications\/(\d+)\/sources/);
  const pubId = page.url().match(/\/publications\/(\d+)/)![1];
  await expect(page.getByText("Fontes sugeridas")).toBeVisible();
  await auditScreen(page, "onboard-step3-sources");
  await page.getByRole("button", { name: "Avançar", exact: true }).click();
  await page.waitForURL(/\/issues\/\d+/, { timeout: 600_000 });
  return pubId;
}

const WIDTH = Number(process.env.CHECK_WIDTH ?? 1440);

test.describe(`klox-68 checks ${WIDTH}px`, () => {
  test.use({ locale: "pt-BR", viewport: { width: WIDTH, height: WIDTH < 768 ? 844 : 900 } });

  test("audit every screen", async ({ page }) => {
    test.setTimeout(900_000);
    const pubId = await onboardToBoard(page);
    await expect(page.locator("p.font-doc").first()).toBeVisible({ timeout: 120_000 });
    await auditScreen(page, "board");

    const issueTab = page.getByRole("button", { name: /Esta peça/ });
    if (await issueTab.isVisible().catch(() => false)) await issueTab.click();
    await page.getByRole("button", { name: /Escrever eu mesmo/ }).click();
    await page.waitForTimeout(800);
    await auditScreen(page, "escrever");

    const issueUrl = page.url().split("?")[0];
    await page.goto(`${issueUrl}?step=revisar`);
    await page.waitForTimeout(800);
    await auditScreen(page, "revisar");

    const visits: Array<[string, string]> = [
      ["edicoes", `/publications/${pubId}`],
      ["leitores", `/publications/${pubId}/readers`],
      ["fontes", `/publications/${pubId}/sources`],
      ["secoes", `/publications/${pubId}/sections`],
      ["configuracoes", `/publications/${pubId}/settings`],
      ["template", `/publications/${pubId}/template`],
      ["guia", `/publications/${pubId}/guide`],
      ["outbox", `/publications/${pubId}/outbox`],
    ];
    for (const [name, to] of visits) {
      await page.goto(to);
      await page.waitForTimeout(700);
      await auditScreen(page, name);
    }
  });
});

test.afterAll(() => {
   
  console.log("CHECKS-JSON " + JSON.stringify(findings));
});
