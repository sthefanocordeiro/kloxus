import type { IssueDocument } from "../api";

// Kept out of docModel.ts: that module is hoisted into the landing chunk
// (KLOX-16 150 KiB budget), and only the issue page needs these.
// writableRefs lists the pauta refs that ask for written text, in
// edition order — link-only items and links-format sections ship the
// link itself, so they never count. The one-pauta-at-a-time flow and
// the "Escrevendo N de M" counter walk this list (KLOX-96).
export function writableRefs(doc: IssueDocument): number[] {
  const refs: number[] = [];
  for (const sec of doc.sections ?? []) {
    for (const it of sec.items ?? []) {
      if ((it.link_only ?? false) || sec.format === "links") continue;
      refs.push(it.ref);
    }
  }
  return refs;
}

// writingProgress is the "Escrevendo N de M" counter: how many writable
// pautas already carry a sentence out of the total that need one.
export function writingProgress(doc: IssueDocument): { done: number; total: number } {
  let done = 0;
  let total = 0;
  for (const sec of doc.sections ?? []) {
    for (const it of sec.items ?? []) {
      if ((it.link_only ?? false) || sec.format === "links") continue;
      total++;
      if ((it.sentences ?? []).length > 0) done++;
    }
  }
  return { done, total };
}
