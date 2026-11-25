// KLOX-90 regression: raw English server messages must never reach the
// pt UI — coded API errors translate through errText.
import { describe, expect, it } from "vitest";
import { ApiRequestError } from "../api";
import { dict, type T } from "../i18n";
import { errText } from "../lib/errors";

// same cast the LangProvider applies — dict.pt shares dict.en's shape
const pt = dict.pt as unknown as T;
const en = dict.en as T;

describe("errText coded errors (KLOX-90)", () => {
  it("localizes known codes in both locales and falls back for unknown ones", () => {
    const e = new ApiRequestError(409, {
      code: "not_approved",
      message: "issue is not approved",
    });
    expect(errText(pt, e)).toBe(dict.pt.errors.not_approved);
    expect(errText(en, e)).toBe(dict.en.errors.not_approved);
    const unknown = new ApiRequestError(409, { code: "brand_new_code", message: "server said no" });
    expect(errText(pt, unknown)).toBe("server said no");
  });
});
