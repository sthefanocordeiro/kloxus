import { describe, expect, it } from "vitest";
import type { Coverage, IssueDocument } from "../api";
import {
  coverageDone,
  coverageLabel,
  docToTiptap,
  failingChecks,
  nextPendingSentence,
  readyFromChecks,
  sectionNeedsCommentary,
} from "../docModel";

const doc = (over: Partial<IssueDocument> = {}): IssueDocument => ({
  issue_id: 1,
  number: 3,
  status: "drafted",
  subject: "Issue",
  version: "v1",
  coverage: { ok: 1, cited: 1, total: 2, flagged: 0 },
  sections: [
    {
      title: "Lead",
      commentary: { mode: "required", text: "" },
      items: [
        {
          ref: 1,
          issue_item_id: 10,
          headline: "Story one",
          sentences: [
            { id: "1.a", text: "Cited fact.", kind: "fact", citations: [1] },
            { id: "1.b", text: "Uncited fact.", kind: "fact", citations: [] },
          ],
          sources: [],
        },
      ],
    },
  ],
  ...over,
});

describe("coverage counter", () => {
  it("renders ok/total", () => {
    expect(coverageLabel({ ok: 11, cited: 12, total: 12, flagged: 0 })).toBe("11/12");
  });

  it("is done only when every fact is cited and unflagged", () => {
    expect(coverageDone({ ok: 4, cited: 4, total: 4, flagged: 0 })).toBe(true);
    expect(coverageDone({ ok: 3, cited: 4, total: 4, flagged: 1 })).toBe(false);
    expect(coverageDone({ ok: 0, cited: 0, total: 0, flagged: 0 })).toBe(false);
  });
});

describe("fix-now target", () => {
  it("points at the first uncited fact sentence", () => {
    expect(nextPendingSentence(doc())).toBe("1.b");
  });

  it("prefers flagged sentences in order", () => {
    const d = doc();
    d.sections[0].items[0].sentences[0].flag = { note: "bad" };
    expect(nextPendingSentence(d)).toBe("1.a");
  });

  it("returns null when nothing needs work", () => {
    const d = doc();
    d.sections[0].items[0].sentences.pop();
    expect(nextPendingSentence(d)).toBeNull();
  });
});

describe("readiness checklist", () => {
  it("is ready only when every check passes", () => {
    expect(readyFromChecks([{ code: "a", ok: true }, { code: "b", ok: true }])).toBe(true);
    expect(readyFromChecks([{ code: "a", ok: true }, { code: "b", ok: false }])).toBe(false);
    expect(readyFromChecks([])).toBe(false);
  });

  it("lists failing checks", () => {
    const checks = [
      { code: "a", ok: true },
      { code: "b", ok: false },
      { code: "c", ok: false },
    ];
    expect(failingChecks(checks).map((c) => c.code)).toEqual(["b", "c"]);
  });
});

describe("commentary", () => {
  it("flags a required empty section", () => {
    expect(sectionNeedsCommentary(doc(), "Lead")).toBe(true);
    const d = doc();
    d.sections[0].commentary.text = "my take";
    expect(sectionNeedsCommentary(d, "Lead")).toBe(false);
    expect(sectionNeedsCommentary(d, "Missing")).toBe(false);
  });
});

describe("docToTiptap", () => {
  it("emits one sentence node per stored sentence, commentary last", () => {
    const tiptap = docToTiptap(doc()) as { content: { type: string }[] };
    const types = tiptap.content.map((n) => n.type);
    expect(types).toEqual(["sectionHeading", "itemHeading", "sentence", "sentence", "commentary"]);
  });

  it("keeps headings out of editable content — titles live in attrs", () => {
    const tiptap = docToTiptap(doc()) as {
      content: { type: string; attrs?: { title?: string; headline?: string }; content?: unknown[] }[];
    };
    const sec = tiptap.content.find((n) => n.type === "sectionHeading");
    const item = tiptap.content.find((n) => n.type === "itemHeading");
    expect(sec?.attrs?.title).toBe("Lead");
    expect(sec?.content).toBeUndefined();
    expect(item?.attrs?.headline).toBe("Story one");
    expect(item?.content).toBeUndefined();
  });

  it("carries sid and citations into node attrs", () => {
    const tiptap = docToTiptap(doc()) as {
      content: { type: string; attrs?: { sid: string; citations: number[] } }[];
    };
    const s = tiptap.content.find((n) => n.type === "sentence");
    expect(s?.attrs?.sid).toBe("1.a");
    expect(s?.attrs?.citations).toEqual([1]);
  });
});

// keep Coverage import used
const _cov: Coverage = { ok: 0, cited: 0, total: 0, flagged: 0 };
void _cov;
