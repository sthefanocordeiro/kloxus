// KLOX-64 locale test: the pt dictionary must be real Portuguese —
// every leaf differs from its English counterpart (proper names
// aside), key structure stays in parity, and the §2 never-show words
// never appear in user-facing copy.
import { describe, expect, it } from "vitest";
import { dict } from "../i18n";

type Leaf = string | number | ((...args: never[]) => string);

// landing.* is skipped in the banned-word scan: the rebuild is owned
// by the landing PR (KLOX-63). Everything else is checked.
function* leaves(
  o: Record<string, unknown>,
  prefix = "",
): Generator<{ path: string; value: Leaf }> {
  for (const [k, v] of Object.entries(o)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (Array.isArray(v)) {
      for (const [i, x] of v.entries()) {
        if (typeof x === "object" && x !== null) {
          yield* leaves(x as Record<string, unknown>, `${path}[${i}]`);
        } else {
          yield { path: `${path}[${i}]`, value: x as Leaf };
        }
      }
    } else if (typeof v === "object" && v !== null) {
      yield* leaves(v as Record<string, unknown>, path);
    } else {
      yield { path, value: v as Leaf };
    }
  }
}

const ARG_SETS: unknown[][] = [[1], [2], [5], [48], ["x"], ["abc"], [1, 2], [2, 3], ["a", "b"], [1, "x"], [new Date(0)]];

function callAll(fn: (...args: never[]) => string): string[] {
  return ARG_SETS.map((args) => {
    try {
      return (fn as (...a: unknown[]) => string)(...args);
    } catch {
      return "∅";
    }
  });
}

// Strings allowed to read identically in both locales: proper names,
// loanwords and brand terms.
const IDENTICAL_OK = new Set(["Radar" /* KLOX-122: same word in both locales */, "Kloxus", "web", "Legal", "Menu", "Links"]);

// Function leaves whose output is language-neutral by design
// (joiners/formatters, not translated copy).
const IDENTICAL_FN_OK = new Set(["board.summaryOf"]);

describe("pt locale ≠ en locale (KLOX-64)", () => {
  const en = dict.en as unknown as Record<string, unknown>;
  const pt = dict.pt as unknown as Record<string, unknown>;
  const enLeaves = new Map([...leaves(en)].map((l) => [l.path, l.value]));
  const ptLeaves = new Map([...leaves(pt)].map((l) => [l.path, l.value]));

  it("has the same key structure in both locales", () => {
    expect([...ptLeaves.keys()].sort()).toEqual([...enLeaves.keys()].sort());
  });

  it("translates every user-facing leaf", () => {
    const equal: string[] = [];
    for (const [path, pv] of ptLeaves) {
      const ev = enLeaves.get(path);
      if (typeof pv === "function" && typeof ev === "function") {
        if (IDENTICAL_FN_OK.has(path)) continue;
        const pouts = callAll(pv as (...a: never[]) => string);
        const eouts = callAll(ev as (...a: never[]) => string);
        if (pouts.every((o, i) => o === eouts[i] && !IDENTICAL_OK.has(o))) {
          equal.push(`${path} (all sampled args)`);
        }
      } else if (pv === ev && !IDENTICAL_OK.has(String(pv))) {
        equal.push(`${path} = ${JSON.stringify(pv)}`);
      }
    }
    expect(equal, `pt leaves identical to en:\n${equal.join("\n")}`).toEqual([]);
  });

  it("never shows the banned words in pt copy", () => {
    // JS \b is ASCII-only: a word ending in an accented letter (dossiê)
    // has no boundary after it, so \b-based patterns miss the exact
    // words §2 bans. Patterns ending in a possible accent close with a
    // (?![\wÀ-ÿ]) lookahead instead, and every inflection is covered
    // (reivindique, sinais, escalações, evidências). Plurals of -al
    // words swap l→is (sinal → sinais), so sina(?:l|is) matches both —
    // no trailing boundary, so sinalização/sinaleiro are caught too.
    // ranque covers the whole ranquear family (ranqueada, ranqueamento,
    // ranqueia); simula covers simulação/simular/simulado — "simule"
    // (rare subjunctive) is scoped out; "simultâneo" must not match.
    const banned = [
      /\bespaços?\b/i,
      /dossi[eê]s?(?![\wÀ-ÿ])/i,
      /evid[êe]ncias?(?![\wÀ-ÿ])/i,
      /\breivindic|\breivindiqu/i,
      /escala[çc](?:[ãa]o|[õo]es)(?![\wÀ-ÿ])/i,
      /\bsina(?:l|is)/i,
      /fonte\(s\)/i,
      /\bdetermin[íi]st/i,
      /\branque/i,
      /\bworkspace\b/i,
      /\bdemo\b/i,
      /\bsimula/i,
      /\bpipeline\b/i,
      /\bissues?\b/i,
    ];
    const hits: string[] = [];
    for (const { path, value } of leaves(pt)) {
      if (path.startsWith("landing.")) continue;
      const outs =
        typeof value === "function" ? callAll(value as (...a: never[]) => string) : [String(value)];
      for (const o of outs) {
        for (const re of banned) {
          if (re.test(o)) hits.push(`${path}: ${o}`);
        }
      }
    }
    expect(hits, `banned words in pt copy:\n${hits.join("\n")}`).toEqual([]);
  });

  it("keeps technical and Portuguese words out of en copy", () => {
    const banned = [
      /\bdeterministic\b/i,
      /\bdossier\b/i,
      /\bworkspace\b/i,
      /\bsignals?\b/i,
      /\bdemo session\b/i,
      /source\(s\)/i,
      // pt glossary words leaking into the en dictionary — same
      // accent-aware boundaries as the pt scan above; -ão nouns also
      // match the -ões plural (edições, seções)
      /\bpautas?\b/i,
      /edi[çc](?:[ãa]o|[õo]es)(?![\wÀ-ÿ])/i,
      /ve[íi]culos?(?![\wÀ-ÿ])/i,
      /\bfontes?\b/i,
      /\bassinantes?\b/i,
      /se[çc](?:[ãa]o|[õo]es)(?![\wÀ-ÿ])/i,
      /dossi[eê]s?(?![\wÀ-ÿ])/i,
      /\breivindic|\breivindiqu/i,
      /\bsina(?:l|is)/i,
    ];
    const hits: string[] = [];
    for (const { path, value } of leaves(en)) {
      if (path.startsWith("landing.")) continue;
      const outs =
        typeof value === "function" ? callAll(value as (...a: never[]) => string) : [String(value)];
      for (const o of outs) {
        for (const re of banned) {
          if (re.test(o)) hits.push(`${path}: ${o}`);
        }
      }
    }
    expect(hits, `banned words in en copy:\n${hits.join("\n")}`).toEqual([]);
  });
});

describe("pt glossary (KLOX-64 §2)", () => {
  const t = dict.pt;

  it("uses the exact required strings", () => {
    expect(t.board.steps).toEqual(["Pautas", "Escrever", "Revisar"]);
    expect(t.coverage.ok).toBe("fatos com fonte");
    expect(t.coverage.fixNow).toBe("Ver pendências");
    expect(t.compose.emptySlot).toBe("Escreva esta pauta");
    expect(t.commentary.required).toBe("Escreva seu comentário (obrigatório nesta seção)");
    expect(t.board.bench).toBe("Sugestões");
    expect(t.evidence.title).toBe("Fontes e fatos");
    expect(t.evidence.empty).toContain("pesquisa");
  });

  it("pluralizes sources correctly — never fonte(s)", () => {
    expect(t.evidence.src(1)).toBe("1 fonte");
    expect(t.evidence.src(2)).toBe("2 fontes");
    expect(t.evidence.src(140)).toBe("140 fontes");
  });

  it("pluralizes draftAction — never '(1 pautas)'", () => {
    expect(t.board.draftAction(1)).toBe("Redigir a peça (1 pauta)");
    expect(t.board.draftAction(2)).toBe("Redigir a peça (2 pautas)");
    expect(t.board.draftAction(7)).toBe("Redigir a peça (7 pautas)");
  });

  it("agrees sectionFull on 'seção', not on the section title", () => {
    expect(t.board.sectionFull("O destaque", 3)).toBe(
      "A seção “O destaque” está cheia (3/3) — escolha uma pauta para trocar",
    );
    expect(t.board.sectionFull("Links rápidos", 4)).toContain(
      "A seção “Links rápidos” está cheia",
    );
  });

  it("speaks second person", () => {
    expect(t.claim.action).toBe("Salvar com seu e-mail");
    expect(t.errors.unclaimed_workspace).toBe("Salve com seu e-mail antes de enviar");
  });
});
