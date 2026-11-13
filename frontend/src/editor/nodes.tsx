import { useEffect, useRef, useState, type MouseEvent } from "react";
import { mergeAttributes, Node } from "@tiptap/core";
import { NodeViewContent, NodeViewWrapper, ReactNodeViewRenderer, type NodeViewProps } from "@tiptap/react";
import { createContext, useContext } from "react";
import { AlertTriangle, PenLine, Quote, Trash2 } from "lucide-react";
import { useT } from "../i18n";
import { cn } from "../lib/cn";
import { Button } from "../ui";
// What the sentence/commentary node views can ask the issue page to do.
export interface DocActions {
  // returns null on success, or the refusal reason to show inline —
  // the edit box stays open with the typed text either way
  editSentence: (sid: string, text: string, citations: number[]) => Promise<string | null>;
  interpret: (sid: string, text?: string) => Promise<boolean>;
  removeSentence: (sid: string) => Promise<boolean>;
  // open the evidence pane on item ref, optionally highlighting a
  // citation's passage and flashing the sentence
  showEvidence: (itemRef: number, opts?: { citeRef?: number; sid?: string; issueItemId?: number }) => void;
  // manual compose: append editor-written text to an item — returns
  // null on success or the refusal reason to show inline
  addSentence: (itemRef: number, text: string, citations: number[], kind: "fact" | "interpretation") => Promise<string | null>;
  readOnly: boolean;
}

// insideCommentary reports whether a resolved position sits inside a
// commentary block — the only editable region in an issue document.
export function insideCommentary($pos: { depth: number; node: (d: number) => { type: { name: string } } | null }): boolean {
  for (let d = $pos.depth; d >= 0; d--) {
    if ($pos.node(d)?.type?.name === "commentary") return true;
  }
  return false;
}

// blurEditor pulls the caret out of the document when a heading is
// clicked — headlines open evidence, they never take text input, so the
// editor must not keep a live selection somewhere a keystroke could
// land outside commentary.
function blurEditor(editor: NodeViewProps["editor"]) {
  editor.commands.blur();
}

export const DocActionsCtx = createContext<DocActions>({
  editSentence: async () => null,
  interpret: async () => false,
  removeSentence: async () => false,
  showEvidence: () => {},
  addSentence: async () => null,
  readOnly: true,
});

interface Src {
  ref: number;
  name: string;
  url: string;
  item_id: number;
  excerpt: string;
  date: string;
}

// SentenceView renders one stored sentence: the words, one chip per
// citation (click → evidence pane with that ref's passage), the flag
// treatment with its three actions — Edit / Mark as interpretation /
// Remove, no "dismiss".
function SentenceView({ node }: NodeViewProps) {
  const { t } = useT();
  const actions = useContext(DocActionsCtx);
  const a = node.attrs as {
    sid: string;
    kind: string;
    citations: number[];
    flag: string | null;
    sources: Src[];
    evidence: { ref: number; excerpt: string }[];
  };
  const text = node.textContent;
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(text);
  const [cites, setCites] = useState<number[]>(a.citations);
  const [editErr, setEditErr] = useState("");
  const [busy, setBusy] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  // register the element so "Fix now" / checklists can scroll + flash
  useEffect(() => {
    if (ref.current) {
      ref.current.dataset.sid = a.sid;
    }
  }, [a.sid]);

  const interpretation = a.kind === "interpretation";
  const itemRef = Number(a.sid.split(".")[0]);

  const run = async (fn: () => Promise<boolean>) => {
    setBusy(true);
    await fn();
    setBusy(false);
    setEditing(false);
  };

  const saveEdit = async () => {
    setBusy(true);
    setEditErr("");
    const err = await actions.editSentence(a.sid, draft, cites);
    setBusy(false);
    if (err === null) {
      setEditing(false);
    } else {
      // refused — keep the box open with the typed text, reason inline
      setEditErr(err);
    }
  };

  const toggleCite = (n: number) =>
    setCites((c) => (c.includes(n) ? c.filter((x) => x !== n) : [...c, n].sort((x, y) => x - y)));

  return (
    <NodeViewWrapper
      ref={ref}
      className={cn(
        "group my-1 rounded-md px-2 py-1 transition-colors",
        a.flag && "bg-warn-soft/70",
        interpretation && "border-l-2 border-line pl-3 italic text-ink-soft",
      )}
    >
      <div className="flex items-start gap-2">
        <p className={cn("flex-1 font-doc leading-7", a.flag && "underline decoration-danger decoration-2 underline-offset-4")}>
          {text}
          {a.citations.map((n) => (
            <button
              key={n}
              contentEditable={false}
              onClick={() => actions.showEvidence(itemRef, { citeRef: n, sid: a.sid })}
              className="mx-0.5 inline-flex h-5 min-w-5 items-center justify-center rounded bg-teal-soft px-1 align-super text-[11px] font-semibold text-teal hover:bg-teal hover:text-paper"
              aria-label={`source ${n}`}
            >
              {n}
            </button>
          ))}
        </p>
        {interpretation && (
          <span className="mt-1 shrink-0 text-[10px] uppercase tracking-wide text-muted" contentEditable={false}>
            {t.doc.interpretationLabel}
          </span>
        )}
      </div>

      {a.flag && !actions.readOnly && (
        <div className="mt-1 flex flex-wrap items-center gap-2" contentEditable={false}>
          <span className="inline-flex items-center gap-1 text-xs text-warn">
            <AlertTriangle size={13} />
            {a.flag}
          </span>
          <span className="flex-1" />
          <Button size="sm" variant="outline" disabled={busy}
            onClick={() => { setDraft(text); setCites(a.citations); setEditErr(""); setEditing(true); }}>
            <PenLine size={12} /> {t.doc.edit}
          </Button>
          <Button size="sm" variant="outline" disabled={busy}
            onClick={() => run(() => actions.interpret(a.sid, a.kind === "interpretation" ? undefined : text))}>
            <Quote size={12} /> {t.doc.interpretation}
          </Button>
          <Button size="sm" variant="danger" disabled={busy} onClick={() => run(() => actions.removeSentence(a.sid))}>
            <Trash2 size={12} /> {t.doc.remove}
          </Button>
        </div>
      )}

      {editing && (
        <div className="mt-2 rounded-md border border-line bg-paper-raised p-2" contentEditable={false}>
          <textarea
            autoFocus
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            rows={2}
            className="w-full resize-y rounded-[var(--radius-input)] border border-line bg-paper p-2 font-doc text-sm leading-6"
          />
          {a.sources.length > 0 && (
            <div className="mt-1.5 flex flex-wrap gap-1">
              {a.sources.map((s) => (
                <button
                  key={s.ref}
                  onClick={() => toggleCite(s.ref)}
                  aria-pressed={cites.includes(s.ref)}
                  className={cn(
                    "rounded px-1.5 py-0.5 text-[11px] font-medium",
                    cites.includes(s.ref) ? "bg-teal text-paper" : "bg-line/60 text-muted hover:bg-line",
                  )}
                  title={s.name}
                >
                  [{s.ref}] {s.name}
                </button>
              ))}
            </div>
          )}
          {editErr && <p className="mt-1.5 text-xs text-danger" role="alert">{editErr}</p>}
          <div className="mt-2 flex gap-2">
            <Button size="sm" disabled={busy} onClick={saveEdit}>
              {t.doc.save}
            </Button>
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => setEditing(false)}>
              {t.doc.cancel}
            </Button>
          </div>
        </div>
      )}
    </NodeViewWrapper>
  );
}

// CommentaryView is the one editable region: labelled so nobody confuses
// it with AI output, gently prompting while a required block is empty.
// The whole card — label, helper text, padding — focuses the content.
function CommentaryView({ node, editor, getPos }: NodeViewProps) {
  const { t } = useT();
  const a = node.attrs as { section: string; mode: string };
  const empty = node.textContent.trim() === "";
  return (
    <NodeViewWrapper
      data-commentary={a.section}
      className="my-3 cursor-text rounded-[var(--radius-card)] border border-dashed border-teal/50 bg-teal-soft/40 p-3"
      onClick={(e: MouseEvent) => {
        // clicks inside the editable text position the cursor naturally;
        // clicks on the label, helper or padding focus the content's end
        if ((e.target as HTMLElement).closest(".commentary-content")) return;
        const pos = typeof getPos === "function" ? getPos() : undefined;
        if (pos != null) {
          editor.chain().focus(pos + node.nodeSize - 1).run();
        }
      }}
    >
      <div className="mb-1 flex items-center gap-2 text-[11px] font-semibold uppercase tracking-wide text-teal" contentEditable={false}>
        {t.commentary.label}
        {a.mode === "required" && (
          <span className="rounded bg-teal px-1.5 py-0.5 text-[9px] text-paper">{t.commentary.requiredTag}</span>
        )}
      </div>
      {empty && a.mode === "required" && (
        <p className="text-xs text-warn" contentEditable={false}>
          {t.commentary.required}
        </p>
      )}
      <NodeViewContent className="commentary-content min-h-16 font-doc text-[15px] leading-7" />
    </NodeViewWrapper>
  );
}

// ItemHeadingView — the story's headline as a non-editable atom; click
// selects it in the evidence pane. The headline lives in attrs so no
// keystroke can rewrite or merge it into a sentence. A manual-compose
// item with no sentences yet gets a dashed "write it" affordance that
// opens the compose box in the evidence pane.
function ItemHeadingView({ node, editor }: NodeViewProps) {
  const { t } = useT();
  const actions = useContext(DocActionsCtx);
  const a = node.attrs as { ref: number; issueItemId: number; headline: string; empty: boolean };
  const open = () => {
    blurEditor(editor);
    actions.showEvidence(a.ref, { issueItemId: a.issueItemId });
  };
  return (
    <NodeViewWrapper className="mt-6" onMouseDown={(e: MouseEvent) => e.preventDefault()} data-item-ref={a.ref}>
      <h3 className="cursor-pointer font-doc text-lg font-semibold hover:text-teal" onClick={open}>
        {a.headline}
      </h3>
      {a.empty && !actions.readOnly && (
        <button
          type="button"
          contentEditable={false}
          onClick={open}
          className="mt-1 inline-flex w-full items-center gap-1.5 rounded-[var(--radius-card)] border border-dashed border-line px-3 py-2 text-left text-xs text-muted transition-colors hover:border-teal/60 hover:text-teal"
        >
          <PenLine size={12} /> {t.compose.emptySlot}
        </button>
      )}
    </NodeViewWrapper>
  );
}

// SectionHeadingView — the section title atom; clicking it only defocuses
// the editor, it is never a text target.
function SectionHeadingView({ node, editor }: NodeViewProps) {
  const a = node.attrs as { title: string };
  return (
    <NodeViewWrapper
      onMouseDown={(e: MouseEvent) => e.preventDefault()}
      onClick={() => blurEditor(editor)}
      data-section-title="1"
    >
      <h2 className="mb-1 mt-8 font-doc text-xl font-semibold">{a.title}</h2>
    </NodeViewWrapper>
  );
}

// --- node definitions ---

// IssueDoc is the issue's top node: the only things that may exist at
// document level are the issue's real parts — section/item heading
// atoms, sentence atoms and commentary blocks. A paragraph can't wrap
// here, so a stray click+type next to a heading inserts nothing instead
// of a phantom paragraph that vanishes on reload.
export const IssueDoc = Node.create({
  name: "doc",
  topNode: true,
  content: "(sectionHeading | itemHeading | sentence | commentary)+",
});

export const Sentence = Node.create({
  name: "sentence",
  group: "block",
  atom: true,
  addAttributes() {
    return {
      sid: { default: "" },
      kind: { default: "fact" },
      citations: { default: [] },
      flag: { default: null },
      sources: { default: [] },
      evidence: { default: [] },
    };
  },
  parseHTML() {
    return [{ tag: "p[data-sid]" }];
  },
  renderHTML({ node, HTMLAttributes }) {
    return ["p", mergeAttributes(HTMLAttributes, { "data-sid": node.attrs.sid }), node.textContent];
  },
  addNodeView() {
    return ReactNodeViewRenderer(SentenceView);
  },
});

export const Commentary = Node.create({
  name: "commentary",
  group: "block",
  content: "paragraph+",
  addAttributes() {
    return {
      section: { default: "" },
      mode: { default: "none" },
    };
  },
  parseHTML() {
    return [{ tag: "div[data-commentary]" }];
  },
  renderHTML({ HTMLAttributes }) {
    return ["div", mergeAttributes(HTMLAttributes, { "data-commentary": "1" }), 0];
  },
  addNodeView() {
    return ReactNodeViewRenderer(CommentaryView);
  },
});

export const ItemHeading = Node.create({
  name: "itemHeading",
  group: "block",
  atom: true,
  selectable: false,
  addAttributes() {
    return { ref: { default: 0 }, issueItemId: { default: 0 }, headline: { default: "" }, empty: { default: false } };
  },
  parseHTML() {
    return [{ tag: "h3[data-item-ref]" }];
  },
  renderHTML({ node, HTMLAttributes }) {
    return ["h3", mergeAttributes(HTMLAttributes, { "data-item-ref": node.attrs.ref }), node.attrs.headline];
  },
  addNodeView() {
    return ReactNodeViewRenderer(ItemHeadingView);
  },
});

// SectionHeading is the non-editable h2 that opens a section — an atom
// like ItemHeading so section names always come from the publication,
// never from typing into the document.
export const SectionHeading = Node.create({
  name: "sectionHeading",
  group: "block",
  atom: true,
  selectable: false,
  addAttributes() {
    return { title: { default: "" } };
  },
  parseHTML() {
    return [{ tag: "h2[data-section-title]" }];
  },
  renderHTML({ node, HTMLAttributes }) {
    return [
      "h2",
      mergeAttributes(HTMLAttributes, {
        "data-section-title": "1",
        class: "mt-8 mb-1 font-doc text-xl font-semibold",
      }),
      node.attrs.title,
    ];
  },
  addNodeView() {
    return ReactNodeViewRenderer(SectionHeadingView);
  },
});
