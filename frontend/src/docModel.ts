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

// mdInline parses the composer text subset — **bold**, _italic_ and
// [label](url) — into Tiptap inline nodes for a rich text block.
export function mdInline(md: string): Record<string, unknown>[] {
  const nodes: Record<string, unknown>[] = [];
  const re = /(\*\*[^*]+\*\*)|(_[^_]+_)|(\[[^\]]*\]\([^)\s]+\))/g;
  let last = 0;
  let m: RegExpExecArray | null;
  const push = (text: string, marks?: Record<string, unknown>[]) => {
    if (text) nodes.push(marks ? { type: "text", text, marks } : { type: "text", text });
  };
  while ((m = re.exec(md))) {
    push(md.slice(last, m.index));
    const tok = m[0];
    if (tok.startsWith("**")) push(tok.slice(2, -2), [{ type: "bold" }]);
    else if (tok.startsWith("_")) push(tok.slice(1, -1), [{ type: "italic" }]);
    else {
      const mm = /^\[([^\]]*)\]\(([^)\s]+)\)$/.exec(tok);
      if (mm) push(mm[1], [{ type: "link", attrs: { href: mm[2] } }]);
      else push(tok);
    }
    last = re.lastIndex;
  }
  push(md.slice(last));
  return nodes;
}

// mdToParas splits a stored multi-line text block into paragraph nodes.
export function mdToParas(md: string): Record<string, unknown>[] {
  const paras = md.split("\n").map((ln) => ln.trim()).filter(Boolean);
  if (paras.length === 0) paras.push("");
  return paras.map((ln) => ({ type: "paragraph", content: mdInline(ln) }));
}

interface InlineMark {
  type: string;
  attrs?: { href?: string };
}

// inlineMd serializes one Tiptap inline node list back to the stored
// markdown subset. A link wins over emphasis — the email renderer does
// not nest marks inside an anchor label.
export function inlineMd(nodes: { type: string; text?: string; marks?: InlineMark[] }[] | undefined): string {
  let out = "";
  for (const n of nodes ?? []) {
    if (n.type !== "text" || !n.text) continue;
    const marks = n.marks ?? [];
    const link = marks.find((m) => m.type === "link")?.attrs?.href;
    if (link) {
      out += `[${n.text}](${link})`;
      continue;
    }
    let s = n.text;
    if (marks.some((m) => m.type === "italic")) s = `_${s}_`;
    if (marks.some((m) => m.type === "bold")) s = `**${s}**`;
    out += s;
  }
  return out;
}

// blockTextMd serializes a blockText node's JSON — paragraphs become
// the stored `> ` lines (one markdown line per paragraph).
export function blockTextMd(node: { content?: { content?: { type: string; text?: string; marks?: InlineMark[] }[] }[] }): string {
  return (node.content ?? [])
    .map((p) => inlineMd(p.content))
    .filter((ln) => ln.trim() !== "")
    .join("\n");
}

// docToTiptap converts the issue document into the Tiptap tree: section
// headings, one commentary node per section, item headings, and one
// sentence node per stored sentence. Attrs carry the API-facing data
// (sid, kind, citations, flag) — node views read them back out. A
// blockAdd gap sits before every block and after the last — insert
// anywhere (KLOX-66 D3).
export function docToTiptap(doc: IssueDocument): Record<string, unknown> {
  const content: Record<string, unknown>[] = [];
  const blockType: Record<string, string> = {
    heading: "blockHeading",
    divider: "blockDivider",
    link: "blockLink",
    image: "blockImage",
    embed: "blockEmbed",
  };
  const gap = (sec: string, index: number) => {
    content.push({ type: "blockAdd", attrs: { section: sec, index } });
  };
  for (const sec of doc.sections ?? []) {
    if (sec.title) {
      content.push({
        type: "sectionHeading",
        attrs: { title: sec.title },
      });
    }
    const byRef = new Map((sec.items ?? []).map((i) => [i.ref, i]));
    // blocks carry the edition's layout order (KLOX-66); documents
    // written before the block model have none — items stand in.
    const blocks = sec.blocks ?? (sec.items ?? []).map((i) => ({ kind: "item" as const, ref: i.ref }));
    blocks.forEach((b, bi) => {
      gap(sec.title, bi);
      if (b.kind === "text") {
        content.push({
          type: "blockText",
          attrs: { section: sec.title, index: bi, count: blocks.length, md: b.text ?? "" },
          content: mdToParas(b.text ?? ""),
        });
        return;
      }
      if (b.kind !== "item") {
        const type = blockType[b.kind];
        if (type) {
          content.push({
            type,
            attrs: { section: sec.title, index: bi, count: blocks.length, text: b.text ?? "", url: b.url ?? "" },
          });
        }
        return;
      }
      const item = byRef.get(b.ref ?? -1);
      if (!item) return;
      content.push({
        type: "itemHeading",
        attrs: {
          ref: item.ref, issueItemId: item.issue_item_id, headline: item.headline,
          section: sec.title, index: bi, format: sec.format ?? "",
          // link-only pauta — item toggle or links-format section —
          // an empty slot asks for no text (KLOX-65/D2, KLOX-66)
          linkOnly: (item.link_only ?? false) || sec.format === "links",
          empty: (item.sentences ?? []).length === 0,
          flagged: (item.sentences ?? []).filter((s) => s.flag).length,
        },
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
    });
    // the last gap — appending lands at the section's end
    gap(sec.title, blocks.length);
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
