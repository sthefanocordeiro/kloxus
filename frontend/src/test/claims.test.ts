import { describe, expect, it } from "vitest";
import { hasNumberDateOrQuote } from "../lib/claims";

describe("hasNumberDateOrQuote", () => {
  it("matches the server rule", () => {
    expect(hasNumberDateOrQuote("Pix bateu 4 bilhões")).toBe(true);
    expect(hasNumberDateOrQuote("Isso aconteceu ontem")).toBe(true);
    expect(hasNumberDateOrQuote("Anunciado amanhã")).toBe(true);
    expect(hasNumberDateOrQuote("Ele disse “sim”")).toBe(true);
    expect(hasNumberDateOrQuote("Uma leitura pessoal sobre o tema")).toBe(false);
  });
});
