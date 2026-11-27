import { describe, expect, it } from "vitest";
import { legalDict } from "../pages/legalText";

function allLegalCopy() {
  return Object.values(legalDict)
    .flatMap((lang) => [lang.terms, lang.privacy])
    .flatMap((doc) => [
      doc.intro,
      ...doc.sections.flatMap((section) => [section.h, ...section.body]),
    ])
    .join("\n");
}

describe("legal copy", () => {
  it("omits the legacy newsletter, paywall and approval positioning", () => {
    expect(allLegalCopy()).not.toMatch(
      /newsletter|paywall|aprova|approval|leitores|readers|checagem|fact/i,
    );
  });

  it("describes the current Radar, export and self-email model in both languages", () => {
    const copy = allLegalCopy();
    expect(copy).toContain("orders the day's news for your Radar");
    expect(copy).toContain("ordena as notícias do dia para o seu Radar");
    expect(copy).toContain("AI does not write your text");
    expect(copy).toContain("A IA não escreve o seu texto");
    expect(copy).toContain("Nothing leaves Kloxus without an export made by you");
    expect(copy).toContain("Nada sai do Kloxus sem uma exportação feita por você");
    expect(copy).toContain("the only email is a daily message to yourself");
    expect(copy).toContain("o único e-mail é o diário para você mesmo");
  });
});
