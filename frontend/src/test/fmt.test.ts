// KLOX-187: story dates read like Hoje's cards — "há 13 h"/"13 h ago"
// under a day old, dd/mm past that, never the raw ISO timestamp.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fmtStoryDate } from "../lib/fmt";

const NOW = new Date("2026-11-24T20:00:00Z");

describe("fmtStoryDate (KLOX-187)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });
  afterEach(() => vi.useRealTimers());

  it("renders 'há N h' under 24 h in pt", () => {
    expect(fmtStoryDate("2026-11-24T07:00:00Z", "pt")).toBe("há 13 h");
    expect(fmtStoryDate("2026-11-24T10:30:00Z", "pt")).toBe("há 10 h");
  });

  it("renders 'N h ago' under 24 h in en", () => {
    expect(fmtStoryDate("2026-11-24T07:00:00Z", "en")).toBe("13 h ago");
    expect(fmtStoryDate("2026-11-24T10:30:00Z", "en")).toBe("10 h ago");
  });

  it("reads the API's naive timestamps as UTC, not local time", () => {
    // "2026-11-24 07:00:00" has no zone marker — parsing it as local
    // shifted every story date by the browser's offset (KLOX-100).
    expect(fmtStoryDate("2026-11-24 07:00:00", "pt")).toBe("há 13 h");
    expect(fmtStoryDate("2026-11-24 07:00:00", "en")).toBe("13 h ago");
  });

  it("floors fresh stories at one hour", () => {
    expect(fmtStoryDate("2026-11-24T19:40:00Z", "pt")).toBe("há 1 h");
    expect(fmtStoryDate("2026-11-24T19:40:00Z", "en")).toBe("1 h ago");
  });

  it("renders dd/mm at and past 24 h in pt", () => {
    // exactly 24 h is no longer "under a day"
    expect(fmtStoryDate("2026-11-23T20:00:00Z", "pt")).toBe("23/11");
    expect(fmtStoryDate("2026-11-10T12:00:00Z", "pt")).toBe("10/11");
  });

  it("renders mm/dd at and past 24 h in en", () => {
    expect(fmtStoryDate("2026-11-23T20:00:00Z", "en")).toBe("11/23");
    expect(fmtStoryDate("2026-11-10T12:00:00Z", "en")).toBe("11/10");
  });

  it("falls back to the date for stories in the future", () => {
    // negative age must not read "há -5 h"
    expect(fmtStoryDate("2026-11-25T20:00:00Z", "pt")).toBe("25/11");
    expect(fmtStoryDate("2026-11-25T20:00:00Z", "en")).toBe("11/25");
  });

  it("returns the input untouched when it is not a date", () => {
    expect(fmtStoryDate("em breve", "pt")).toBe("em breve");
    expect(fmtStoryDate("not a date", "en")).toBe("not a date");
    expect(fmtStoryDate("", "pt")).toBe("");
  });
});
