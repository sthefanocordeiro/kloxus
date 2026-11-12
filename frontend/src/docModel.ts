import type { Check, Coverage, IssueDocument } from "./api";

// Pure helpers over the issue document — the pieces the unit tests pin.

// coverageLabel renders the always-visible counter, e.g. "11/12".
export function coverageLabel(c: Coverage): string {
  return `${c.ok}/${c.total}`;
}

// coverageDone: every fact sentence is cited and unflagged.
export function coverageDone(c: Coverage): boolean {
  return c.total > 0 && c.ok === c.total && c.flagged === 0;
}

// nextPendingSentence returns the first sentence id that still needs
// work (flagged, or a fact with no citations) in document order — the
// target of the "Fix now" button.
export function nextPendingSentence(doc: IssueDocument): string | null {
  for (const sec of doc.sections ?? []) {
    for (const item of sec.items ?? []) {
      for (const s of item.sentences ?? []) {
        if (s.flag || (s.kind === "fact" && (s.citations ?? []).length === 0)) {
          return s.id;
        }
      }
    }
  }
  return null;
}

// sectionNeedsCommentary: a required-and-empty commentary block.
export function sectionNeedsCommentary(doc: IssueDocument, title: string): boolean {
  const sec = doc.sections.find((s) => s.title === title);
  return !!sec && sec.commentary.mode === "required" && sec.commentary.text.trim() === "";
}

// readyFromChecks mirrors the readiness gate used by the checklist:
// every check green. Exposed for tests; the server remains authoritative.
export function readyFromChecks(checks: Check[]): boolean {
  return checks.length > 0 && checks.every((c) => c.ok);
}

// failingChecks returns the checks that still block approval.
export function failingChecks(checks: Check[]): Check[] {
  return checks.filter((c) => !c.ok);
}

// docToTiptap converts the issue document into the Tiptap tree: section
// headings, one commentary node per section, item headings, and one
// sentence node per stored sentence. Attrs carry the API-facing data
// (sid, kind, citations, flag) — node views read them back out.
export function docToTiptap(doc: IssueDocument): Record<string, unknown> {
  const content: Record<string, unknown>[] = [];
  for (const sec of doc.sections ?? []) {
    if (sec.title) {
      content.push({
        type: "sectionHeading",
        attrs: { title: sec.title },
      });
    }
    for (const item of sec.items ?? []) {
      content.push({
        type: "itemHeading",
        attrs: { ref: item.ref, issueItemId: item.issue_item_id, headline: item.headline },
      });
      for (const s of item.sentences ?? []) {
        content.push({
          type: "sentence",
          attrs: {
            sid: s.id,
            kind: s.kind,
            citations: s.citations ?? [],
            flag: s.flag?.note ?? null,
            sources: item.sources ?? [],
            evidence: s.evidence ?? [],
          },
          content: [{ type: "text", text: s.text }],
        });
      }
    }
    // commentary closes the section, where it appears in the output
    if (sec.commentary.mode !== "none") {
      content.push({
        type: "commentary",
        attrs: { section: sec.title, mode: sec.commentary.mode },
        content: [
          { type: "paragraph", content: sec.commentary.text ? [{ type: "text", text: sec.commentary.text }] : [] },
        ],
      });
    }
  }
  return { type: "doc", content };
}
