// KLOX-90 regression: raw English server messages must never reach the
// pt UI — coded API errors translate through errText, and the bare
// pipeline failure a finished run carries maps through runErrText.
import { describe, expect, it } from "vitest";
import { ApiRequestError } from "../api";
import { dict, type T } from "../i18n";
import { errText, isNoItemsFailure, runErrText } from "../lib/errors";

const NO_ITEMS = "no items collected — check the publication's sources";
// same cast the LangProvider applies — dict.pt shares dict.en's shape
const pt = dict.pt as unknown as T;
const en = dict.en as T;

describe("errText coded errors (KLOX-90)", () => {
  it("localizes interpretation_restricted in both locales", () => {
    const e = new ApiRequestError(409, {
      code: "interpretation_restricted",
      message: "a claim with a number, a date or a quotation cannot be marked as interpretation — fix it or remove it",
    });
    expect(errText(pt, e)).toBe(dict.pt.errors.interpretation_restricted);
    expect(errText(pt, e)).not.toContain("interpretation");
    expect(errText(en, e)).toBe(dict.en.errors.interpretation_restricted);
  });
});

describe("runErrText (KLOX-90)", () => {
  it("localizes the empty-harvest pipeline failure", () => {
    expect(isNoItemsFailure(NO_ITEMS)).toBe(true);
    expect(runErrText(pt, NO_ITEMS)).toBe(dict.pt.run.noItems);
    expect(runErrText(en, NO_ITEMS)).toBe(dict.en.run.noItems);
  });

  it("passes unknown run messages through and defaults when empty", () => {
    expect(runErrText(pt, "pipeline panic: boom")).toBe("pipeline panic: boom");
    expect(runErrText(pt, undefined)).toBe(dict.pt.errors.generic);
    expect(isNoItemsFailure(undefined)).toBe(false);
  });
});
