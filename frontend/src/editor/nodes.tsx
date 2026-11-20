import { useEffect, useRef, useState, type FC, type MouseEvent } from "react";
import { mergeAttributes, Node } from "@tiptap/core";
import { NodeViewContent, NodeViewWrapper, ReactNodeViewRenderer, type NodeViewProps } from "@tiptap/react";
import { useContext } from "react";
import { AlertTriangle, ArrowDown, ArrowUp, Bold, Italic, Link2, PenLine, Plus, Quote, Trash2 } from "lucide-react";
import { flagNote, sectionTitle, useT } from "../i18n";
import { useComposer } from "./composerStrings";
import { cn } from "../lib/cn";
import { Button } from "../ui";
// The DocActions contract lives in ./actions so the compose surface
// (components/Compose) can consume it without a runtime import cycle
// back into this module — re-exported here so existing imports keep
// working.
export { DocActionsCtx } from "./actions";
export type { ComposeDraft, ComposeInsert, DocActions } from "./actions";
import { DocActionsCtx, type ComposeInsert } from "./actions";
import { ComposeBox } from "../components/Compose";

// insideCommentary reports whether a resolved position sits inside a
// commentary block — a legacy name for the sentence-level guard; the
// composer guard is insideEditable (commentary OR a rich text block).
export function insideCommentary($pos: { depth: number; node: (d: number) => { type: { name: string } } | null }): boolean {
  for (let d = $pos.depth; d >= 0; d--) {
    if ($pos.node(d)?.type?.name === "commentary") return true;
  }
  return false;
}

// insideEditable: keystrokes land only inside a commentary card or a
// rich text block — every other node stays a non-editable atom.
export function insideEditable($pos: { depth: number; node: (d: number) => { type: { name: string } } | null }): boolean {
  for (let d = $pos.depth; d >= 0; d--) {
    const name = $pos.node(d)?.type?.name;
    if (name === "commentary" || name === "blockText") return true;
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

// FIX_SENTENCE_EVENT is dispatched on a sentence's wrapper element by
// "Fix now" and the readiness checklist — the sentence opens its edit
// affordance instead of only flashing (KLOX-52).
export const FIX_SENTENCE_EVENT = "kloxus:fix-sentence";

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
  const { t, lang } = useT();
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
  // tools pins the edit/remove row open — a click reveals it (touch has
  // no hover); hover alone also reveals it via group-hover (KLOX-96)
  const [tools, setTools] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  // register the element so "Fix now" / checklists can scroll + flash
  useEffect(() => {
    if (ref.current) {
      ref.current.dataset.sid = a.sid;
    }
  }, [a.sid]);

  // ...and can open this sentence's fix affordance — the edit box with
  // its citation chips is where an uncited or unsupported claim is fixed
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const open = () => {
      if (actions.readOnly) return;
      setDraft(text);
      setCites(a.citations);
      setEditErr("");
      setEditing(true);
    };
    el.addEventListener(FIX_SENTENCE_EVENT, open);
    return () => el.removeEventListener(FIX_SENTENCE_EVENT, open);
  }, [actions.readOnly, text, a.citations]);

  const interpretation = a.kind === "interpretation";
  const itemRef = Number(a.sid.split(".")[0]);

  const run = async (fn: () => Promise<boolean>) => {
    setBusy(true);
    try {
      await fn();
    } finally {
      setBusy(false);
      setEditing(false);
    }
  };

  const openEdit = () => {
    setDraft(text);
    setCites(a.citations);
    setEditErr("");
    setEditing(true);
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

  // the same three actions flagged sentences always carried — the flag
  // row keeps them visible, every other sentence gets them on
  // click/hover/double-click (KLOX-96: accepted phrases are editable
  // and removable too)
  const actionsRow = (
    <span className="ml-auto flex items-center gap-2">
      <Button size="sm" variant="outline" disabled={busy} onClick={openEdit}>
        <PenLine size={12} /> {t.doc.edit}
      </Button>
      <Button size="sm" variant="outline" disabled={busy}
        onClick={() => run(() => actions.interpret(a.sid, a.kind === "interpretation" ? undefined : text))}>
        <Quote size={12} /> {t.doc.interpretation}
      </Button>
      <Button size="sm" variant="danger" disabled={busy} onClick={() => run(() => actions.removeSentence(a.sid))}>
        <Trash2 size={12} /> {t.doc.remove}
      </Button>
    </span>
  );

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
        <p
          className={cn("flex-1 font-doc leading-7", a.flag && "underline decoration-danger decoration-2 underline-offset-4")}
          title={actions.readOnly ? undefined : t.doc.editHint}
          onClick={() => {
            if (!actions.readOnly) setTools((v) => !v);
          }}
          onDoubleClick={() => {
            if (!actions.readOnly) openEdit();
          }}
        >
          {text}
          {a.citations.map((n) => (
            <button
              key={n}
              contentEditable={false}
              onClick={(e) => {
                e.stopPropagation();
                actions.showEvidence(itemRef, { citeRef: n, sid: a.sid });
              }}
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
            {flagNote(lang, a.flag)}
          </span>
          {actionsRow}
        </div>
      )}

      {!a.flag && !actions.readOnly && (
        <div
          className={cn("mt-1 flex-wrap items-center gap-2", tools ? "flex" : "hidden group-hover:flex")}
          contentEditable={false}
        >
          {actionsRow}
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
// keystroke can rewrite or merge it into a sentence. The pauta's
// writing surface lives right under it (KLOX-96): "Escreva esta pauta"
// opens the inline composer in the document itself, and a written item
// keeps an "add a sentence" affordance — the side pane is supporting
// material, never the editor. Link-only items (links-format sections)
// are exempt: the link is the source (D2).
function ItemHeadingView({ node, editor }: NodeViewProps) {
  const { t } = useT();
  const b = useComposer();
  const actions = useContext(DocActionsCtx);
  const a = node.attrs as {
    ref: number; issueItemId: number; headline: string; empty: boolean; flagged: number;
    linkOnly: boolean; format: string; sources: Src[];
  };
  const [busy, setBusy] = useState(false);
  const [insert, setInsert] = useState<ComposeInsert | null>(null);
  const linkItem = a.linkOnly || a.format === "links";
  // the page owns which item's composer is open so it survives document
  // refetches (every node view remounts on each add)
  const composing = actions.composeRef === a.ref && !linkItem && !actions.readOnly;
  // the latest prefill request for this item seeds the box — click a
  // fact or a source excerpt in the rail and it lands here. Consume it
  // on pickup: every document save remounts this view, and a pending
  // insert must not seed a second time (KLOX-96)
  const { composeInsert: ci, consumeInsert } = actions;
  useEffect(() => {
    if (ci && ci.ref === a.ref) {
      setInsert(ci.insert);
      consumeInsert(a.ref);
    }
  }, [ci, a.ref, consumeInsert]);
  // a closed composer forgets the delivered insert — the parked draft
  // already carries its text; reseeding on reopen would duplicate it
  useEffect(() => {
    if (!composing) setInsert(null);
  }, [composing]);
  const open = () => {
    blurEditor(editor);
    actions.showEvidence(a.ref, { issueItemId: a.issueItemId });
  };
  const writeHere = () => {
    // wide screens pair the composer with the item's facts in the rail;
    // on phone the doc keeps the surface — the rail is a tab away
    if (window.innerWidth >= 768) open();
    else blurEditor(editor);
    actions.composeFor(a.ref);
  };
  return (
    <NodeViewWrapper className="group mt-6" onMouseDown={(e: MouseEvent) => e.preventDefault()} data-item-ref={a.ref}>
      <div className="flex items-start justify-between gap-2">
        <h3 className="cursor-pointer font-doc text-lg font-semibold hover:text-teal" onClick={open}>
          {a.headline}
          {linkItem && (
            <span className="ml-2 inline-flex translate-y-[-1px] items-center gap-0.5 rounded bg-line/60 px-1.5 py-0.5 align-middle text-[10px] font-medium uppercase tracking-wide text-muted">
              <Link2 size={10} /> {b.linkOnly}
            </span>
          )}
        </h3>
        <span className="flex shrink-0 items-center gap-1">
          {!a.empty && !linkItem && !actions.readOnly && (
            <button
              type="button"
              contentEditable={false}
              disabled={busy}
              title={t.compose.addLine}
              aria-label={t.compose.addLine}
              className="rounded p-1 text-muted transition-opacity hover:text-teal focus-visible:opacity-100 md:opacity-0 md:group-hover:opacity-100"
              onClick={writeHere}
            >
              <PenLine size={13} />
            </button>
          )}
          {!actions.readOnly && (
            <button
              type="button"
              contentEditable={false}
              disabled={busy}
              title={linkItem ? b.linkOnlyOff : b.linkOnlyOn}
              aria-pressed={a.linkOnly}
              className={cn(
                "rounded p-1 transition-opacity",
                a.linkOnly ? "text-teal" : "text-muted opacity-0 hover:text-teal group-hover:opacity-100",
              )}
              onClick={async () => {
                setBusy(true);
                try {
                  await actions.toggleLinkOnly(a.ref, !a.linkOnly);
                } finally {
                  setBusy(false);
                }
              }}
            >
              <Link2 size={13} />
            </button>
          )}
          {a.flagged > 0 && !actions.readOnly && (
            <Button
              size="sm"
              variant="danger"
              disabled={busy}
              contentEditable={false}
              onClick={async () => {
                setBusy(true);
                try {
                  await actions.removeFlagged(a.ref);
                } finally {
                  setBusy(false);
                }
              }}
            >
              <Trash2 size={12} /> {t.doc.removeFlagged(a.flagged)}
            </Button>
          )}
        </span>
      </div>
      {/* a link-only pauta ships the link itself — never a "write it"
          affordance, never a citation prompt (KLOX-66 D2) */}
      {a.empty && !linkItem && !actions.readOnly && !composing && (
        <button
          type="button"
          contentEditable={false}
          onClick={writeHere}
          className="mt-1 inline-flex w-full items-center gap-1.5 rounded-[var(--radius-card)] border border-dashed border-line px-3 py-2 text-left text-xs text-muted transition-colors hover:border-teal/60 hover:text-teal"
        >
          <PenLine size={12} /> {t.compose.emptySlot}
        </button>
      )}
      {/* the composer is the document: mousedown must not bubble to the
          wrapper's preventDefault or the textarea never takes focus */}
      {composing && (
        <div contentEditable={false} onMouseDown={(e: MouseEvent) => e.stopPropagation()} className="mt-1">
          <ComposeBox
            item={{ ref: a.ref, sources: a.sources ?? [] }}
            empty={a.empty}
            insert={insert}
            onClose={() => actions.composeClose(a.ref)}
          />
        </div>
      )}
    </NodeViewWrapper>
  );
}

// SectionHeadingView — the section title atom; clicking it only defocuses
// the editor, it is never a text target.
function SectionHeadingView({ node, editor }: NodeViewProps) {
  const { lang } = useT();
  const a = node.attrs as { title: string };
  return (
    <NodeViewWrapper
      onMouseDown={(e: MouseEvent) => e.preventDefault()}
      onClick={() => blurEditor(editor)}
      data-section-title="1"
    >
      <h2 className="mb-1 mt-8 font-doc text-xl font-semibold">{sectionTitle(lang, a.title)}</h2>
    </NodeViewWrapper>
  );
}

// --- composer blocks (KLOX-66) ---
// A section is an ordered list of blocks: pauta items plus
// author-made heading / divider / link / text blocks. Block attrs
// carry (section, index) — the pair the block API ops address.

// BlockTools is the shared hover toolbar on every block: move up,
// move down, remove. Buttons live outside the editable flow.
function BlockTools({ node }: { node: NodeViewProps["node"] }) {
  const actions = useContext(DocActionsCtx);
  const { t } = useT();
  const b = useComposer();
  const a = node.attrs as { section: string; index: number; count: number };
  const [busy, setBusy] = useState(false);
  if (actions.readOnly) return null;
  const run = (fn: () => Promise<boolean>) => {
    setBusy(true);
    void fn().finally(() => setBusy(false));
  };
  return (
    <span className="ml-2 inline-flex shrink-0 items-center gap-0.5 align-middle opacity-0 transition-opacity group-hover:opacity-100" contentEditable={false}>
      <button type="button" disabled={busy || a.index === 0} aria-label={b.up}
        className="rounded p-1 text-muted hover:bg-line/60 hover:text-ink disabled:opacity-40"
        onClick={() => run(() => actions.moveBlock(a.section, a.index, -1))}>
        <ArrowUp size={12} />
      </button>
      <button type="button" disabled={busy || a.index >= a.count - 1} aria-label={b.down}
        className="rounded p-1 text-muted hover:bg-line/60 hover:text-ink disabled:opacity-40"
        onClick={() => run(() => actions.moveBlock(a.section, a.index, 1))}>
        <ArrowDown size={12} />
      </button>
      <button type="button" disabled={busy} aria-label={t.doc.remove}
        className="rounded p-1 text-muted hover:bg-line/60 hover:text-danger disabled:opacity-40"
        onClick={() => run(() => actions.removeBlock(a.section, a.index))}>
        <Trash2 size={12} />
      </button>
    </span>
  );
}

// useBlockEdit is the inline edit state shared by the text-bearing
// blocks (heading / link / text) — open an edit row, save through
// updateBlock, keep the refusal inline.
function useBlockEdit(node: NodeViewProps["node"]) {
  const actions = useContext(DocActionsCtx);
  const a = node.attrs as { section: string; index: number; text: string; url: string };
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(a.text);
  const [url, setUrl] = useState(a.url);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const open = () => { setText(a.text); setUrl(a.url); setErr(""); setEditing(true); };
  const save = async () => {
    setBusy(true);
    const refused = await actions.updateBlock(a.section, a.index, text, url);
    setBusy(false);
    if (refused === null) setEditing(false);
    else setErr(refused);
  };
  return { editing, text, setText, url, setUrl, err, busy, open, save, close: () => setEditing(false) };
}

function BlockEditRow({ e, url, busy }: {
  e: ReturnType<typeof useBlockEdit>; url?: boolean; busy?: boolean;
}) {
  const { t } = useT();
  return (
    <div className="mt-1 space-y-1" contentEditable={false}>
      <input
        autoFocus
        value={e.text}
        onChange={(ev) => e.setText(ev.target.value)}
        onKeyDown={(ev) => { if (ev.key === "Enter" && !url) void e.save(); if (ev.key === "Escape") e.close(); }}
        className="w-full rounded-[var(--radius-input)] border border-line bg-paper px-2 py-1 font-doc text-sm leading-6"
      />
      {url && (
        <input
          value={e.url}
          onChange={(ev) => e.setUrl(ev.target.value)}
          onKeyDown={(ev) => { if (ev.key === "Enter") void e.save(); if (ev.key === "Escape") e.close(); }}
          placeholder="https://"
          className="w-full rounded-[var(--radius-input)] border border-line bg-paper px-2 py-1 text-xs text-muted"
        />
      )}
      {e.err && <p className="text-xs text-danger" role="alert">{e.err}</p>}
      <div className="flex gap-2">
        <Button size="sm" disabled={e.busy || busy} onClick={() => void e.save()}>{t.doc.save}</Button>
        <Button size="sm" variant="ghost" onClick={e.close}>{t.doc.cancel}</Button>
      </div>
    </div>
  );
}

function BlockHeadingView({ node }: NodeViewProps) {
  const actions = useContext(DocActionsCtx);
  const e = useBlockEdit(node);
  const a = node.attrs as { text: string };
  return (
    <NodeViewWrapper className="group my-2 flex items-start">
      <h4 className="font-doc text-base font-semibold text-teal">{a.text}</h4>
      <BlockTools node={node} />
      {!actions.readOnly && !e.editing && (
        <button type="button" contentEditable={false} aria-label="edit"
          className="ml-1 rounded p-1 text-muted opacity-0 hover:text-ink group-hover:opacity-100"
          onClick={e.open}>
          <PenLine size={12} />
        </button>
      )}
      {e.editing && <div className="flex-1"><BlockEditRow e={e} /></div>}
    </NodeViewWrapper>
  );
}

function BlockDividerView({ node }: NodeViewProps) {
  return (
    <NodeViewWrapper className="group my-4 flex items-center">
      <hr className="flex-1 border-line" />
      <BlockTools node={node} />
    </NodeViewWrapper>
  );
}

function BlockLinkView({ node }: NodeViewProps) {
  const actions = useContext(DocActionsCtx);
  const e = useBlockEdit(node);
  const a = node.attrs as { text: string; url: string };
  return (
    <NodeViewWrapper className="group my-1 flex items-start">
      <p className="flex-1 font-doc text-[15px] leading-7">
        <span className="mr-1.5 text-muted">•</span>
        <a href={a.url} target="_blank" rel="noreferrer" className="text-teal underline decoration-teal/40 hover:decoration-teal" contentEditable={false}>
          {a.text}
        </a>
      </p>
      <BlockTools node={node} />
      {!actions.readOnly && !e.editing && (
        <button type="button" contentEditable={false} aria-label="edit"
          className="ml-1 rounded p-1 text-muted opacity-0 hover:text-ink group-hover:opacity-100"
          onClick={e.open}>
          <PenLine size={12} />
        </button>
      )}
      {e.editing && <div className="flex-1"><BlockEditRow e={e} url /></div>}
    </NodeViewWrapper>
  );
}

// BlockTextView is the free-form rich text block (KLOX-66 D3): real
// inline editing with bold / italic / link marks. The stored form is
// the `> text` markdown subset — the page autosaves the diff.
function BlockTextView({ node, editor }: NodeViewProps) {
  const { t } = useT();
  const b = useComposer();
  const [linkOpen, setLinkOpen] = useState(false);
  const [href, setHref] = useState("");
  const applyLink = () => {
    const u = href.trim();
    if (u) {
      editor.chain().focus().extendMarkRange("link").setLink({ href: u }).run();
    } else {
      editor.chain().focus().extendMarkRange("link").unsetLink().run();
    }
    setLinkOpen(false);
  };
  return (
    <NodeViewWrapper className="group my-1 rounded-md px-2 py-1">
      <div contentEditable={false}
        className="mb-0.5 flex items-center gap-0.5 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
        <button type="button" aria-label="bold" title={b.bold}
          className={cn("rounded p-1 text-muted hover:bg-line/60 hover:text-ink", editor.isActive("bold") && "bg-line/60 text-ink")}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => editor.chain().focus().toggleBold().run()}>
          <Bold size={12} />
        </button>
        <button type="button" aria-label="italic" title={b.italic}
          className={cn("rounded p-1 text-muted hover:bg-line/60 hover:text-ink", editor.isActive("italic") && "bg-line/60 text-ink")}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => editor.chain().focus().toggleItalic().run()}>
          <Italic size={12} />
        </button>
        <button type="button" aria-label="link" title={b.link}
          className={cn("rounded p-1 text-muted hover:bg-line/60 hover:text-ink", editor.isActive("link") && "bg-line/60 text-ink")}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => { setHref(String(editor.getAttributes("link").href ?? "")); setLinkOpen((v) => !v); }}>
          <Link2 size={12} />
        </button>
        <BlockTools node={node} />
      </div>
      {linkOpen && (
        <div contentEditable={false} className="mb-1 flex items-center gap-1">
          <input
            autoFocus
            value={href}
            onChange={(e) => setHref(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") applyLink(); if (e.key === "Escape") setLinkOpen(false); }}
            placeholder="https://"
            className="w-64 rounded-[var(--radius-input)] border border-line bg-paper px-2 py-1 text-xs"
          />
          <Button size="sm" onClick={applyLink}>{t.doc.save}</Button>
          <Button size="sm" variant="ghost" onClick={() => setLinkOpen(false)}>{t.doc.cancel}</Button>
        </div>
      )}
      <NodeViewContent className="font-doc text-[15px] leading-7 text-ink-soft [&_p]:my-0.5" />
    </NodeViewWrapper>
  );
}

// BlockImageView renders a composer image (banner or inline photo):
// the stored ![alt](src) line. Edit swaps src/alt through updateBlock;
// upload goes through the page's media endpoint (D3).
function BlockImageView({ node }: NodeViewProps) {
  const { t } = useT();
  const b = useComposer();
  const actions = useContext(DocActionsCtx);
  const a = node.attrs as { section: string; index: number; text: string; url: string };
  const [editing, setEditing] = useState(false);
  const [url, setUrl] = useState(a.url);
  const [alt, setAlt] = useState(a.text);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const save = async () => {
    setBusy(true);
    const refused = await actions.updateBlock(a.section, a.index, alt, url);
    setBusy(false);
    if (refused === null) setEditing(false);
    else setErr(refused);
  };
  const onFile = async (f: File | undefined) => {
    if (!f) return;
    setBusy(true);
    const buf = await f.arrayBuffer();
    let bin = "";
    new Uint8Array(buf).forEach((b) => (bin += String.fromCharCode(b)));
    const res = await actions.uploadImage(btoa(bin), f.type || "image/png", f.name);
    setBusy(false);
    if (res) setUrl(res);
    else setErr(b.uploadErr);
  };
  return (
    <NodeViewWrapper className="group my-3" contentEditable={false}>
      <figure>
        <img src={a.url} alt={a.text} className="max-h-96 w-full rounded-[var(--radius-card)] object-cover" />
        {a.text && <figcaption className="mt-1 text-center text-xs text-muted">{a.text}</figcaption>}
      </figure>
      {!actions.readOnly && !editing && (
        <div className="mt-1 flex items-center opacity-0 transition-opacity group-hover:opacity-100">
          <button type="button" aria-label="edit"
            className="rounded p-1 text-muted hover:bg-line/60 hover:text-ink"
            onClick={() => { setUrl(a.url); setAlt(a.text); setErr(""); setEditing(true); }}>
            <PenLine size={12} />
          </button>
          <BlockTools node={node} />
        </div>
      )}
      {editing && (
        <div className="mt-2 max-w-md space-y-1.5 rounded-[var(--radius-card)] border border-line bg-paper-raised p-2">
          <div className="flex gap-1">
            <input
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder={b.imageUrlPh}
              className="min-w-0 flex-1 rounded-[var(--radius-input)] border border-line bg-paper px-2 py-1 text-xs"
            />
            <Button size="sm" variant="outline" disabled={busy} onClick={() => fileRef.current?.click()}>
              {b.upload}
            </Button>
            <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/gif,image/webp" className="hidden"
              onChange={(e) => { void onFile(e.target.files?.[0]); e.target.value = ""; }} />
          </div>
          <input
            value={alt}
            onChange={(e) => setAlt(e.target.value)}
            placeholder={b.altPh}
            className="w-full rounded-[var(--radius-input)] border border-line bg-paper px-2 py-1 text-xs"
          />
          {err && <p className="text-xs text-danger" role="alert">{err}</p>}
          <div className="flex gap-2">
            <Button size="sm" disabled={busy || !url.trim()} onClick={() => void save()}>{t.doc.save}</Button>
            <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>{t.doc.cancel}</Button>
          </div>
        </div>
      )}
    </NodeViewWrapper>
  );
}

// BlockEmbedView renders a sanitized HTML/embed block. The stored text
// is already sanitized server-side — the canvas preview injects it as
//-is, the pencil opens the raw field for edits (D3 trust boundary:
// only the sanitized form reaches any surface).
function BlockEmbedView({ node }: NodeViewProps) {
  const { t } = useT();
  const b = useComposer();
  const actions = useContext(DocActionsCtx);
  const a = node.attrs as { section: string; index: number; text: string };
  const [editing, setEditing] = useState(false);
  const [html, setHtml] = useState(a.text);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    const refused = await actions.updateBlock(a.section, a.index, html);
    setBusy(false);
    if (refused === null) setEditing(false);
    else setErr(refused);
  };
  return (
    <NodeViewWrapper className="group my-3 rounded-[var(--radius-card)] border border-line p-2" contentEditable={false}>
      <div className="embed-preview" dangerouslySetInnerHTML={{ __html: a.text }} />
      {!actions.readOnly && !editing && (
        <div className="mt-1 flex items-center opacity-0 transition-opacity group-hover:opacity-100">
          <button type="button" aria-label="edit"
            className="rounded p-1 text-muted hover:bg-line/60 hover:text-ink"
            onClick={() => { setHtml(a.text); setErr(""); setEditing(true); }}>
            <PenLine size={12} />
          </button>
          <BlockTools node={node} />
        </div>
      )}
      {editing && (
        <div className="mt-2 space-y-1.5">
          <textarea
            autoFocus
            value={html}
            onChange={(e) => setHtml(e.target.value)}
            rows={6}
            placeholder={b.embedPh}
            className="w-full resize-y rounded-[var(--radius-input)] border border-line bg-paper p-2 font-mono text-xs leading-5"
          />
          {err && <p className="text-xs text-danger" role="alert">{err}</p>}
          <div className="flex gap-2">
            <Button size="sm" disabled={busy || !html.trim()} onClick={() => void save()}>{t.doc.save}</Button>
            <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>{t.doc.cancel}</Button>
          </div>
        </div>
      )}
    </NodeViewWrapper>
  );
}

// BlockAddView is the insertion gap between every pair of blocks
// (KLOX-66 D3): a slim hover affordance that opens the block menu —
// text, heading, link, divider, image, embed, standard message and
// pauta — inserting at the gap's own position.
function BlockAddView({ node }: NodeViewProps) {
  const actions = useContext(DocActionsCtx);
  const { t } = useT();
  const b = useComposer();
  const a = node.attrs as { section: string; index: number };
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<"" | "heading" | "link" | "text">("");
  const [text, setText] = useState("");
  const [url, setUrl] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  if (actions.readOnly) return <NodeViewWrapper className="hidden" />;
  const close = () => { setOpen(false); setKind(""); setText(""); setUrl(""); setErr(""); };
  const insert = async (k: "heading" | "divider" | "link" | "text", tx = "", u = "") => {
    setBusy(true);
    const refused = await actions.insertBlock(a.section, k, a.index, tx, u);
    setBusy(false);
    if (refused === null) close();
    else setErr(refused);
  };
  return (
    <NodeViewWrapper className="group/gap relative my-0.5" contentEditable={false}>
      {!open && !kind && (
        <div className="flex h-4 items-center justify-center">
          <span className="absolute inset-x-2 top-1/2 h-px bg-transparent transition-colors group-hover/gap:bg-line" />
          <button type="button" onClick={() => setOpen(true)} aria-label={b.add}
            className="relative rounded-full border border-transparent bg-paper p-0.5 text-transparent transition-colors hover:border-teal hover:text-teal group-hover/gap:border-line group-hover/gap:text-muted">
            <Plus size={12} />
          </button>
        </div>
      )}
      {(open || kind) && (
        <div className="my-1 rounded-[var(--radius-card)] border border-line bg-paper-raised p-2">
          {!kind && (
            <div className="flex flex-wrap items-center gap-1.5">
              {(
                [
                  ["text", b.text],
                  ["heading", b.heading],
                  ["link", b.link],
                  ["divider", b.divider],
                ] as const
              ).map(([k, label]) => (
                <button key={k} type="button" disabled={busy}
                  className="rounded-full border border-line px-2.5 py-1 text-xs text-ink hover:border-teal hover:text-teal"
                  onClick={() => (k === "divider" ? void insert("divider") : setKind(k))}>
                  {label}
                </button>
              ))}
              <button type="button" disabled={busy}
                className="rounded-full border border-line px-2.5 py-1 text-xs text-ink hover:border-teal hover:text-teal"
                onClick={() => { close(); actions.openInsert(a.section, a.index, "image"); }}>
                {b.image}
              </button>
              <button type="button" disabled={busy}
                className="rounded-full border border-line px-2.5 py-1 text-xs text-ink hover:border-teal hover:text-teal"
                onClick={() => { close(); actions.openInsert(a.section, a.index, "embed"); }}>
                {b.embed}
              </button>
              <button type="button" disabled={busy}
                className="rounded-full border border-line px-2.5 py-1 text-xs text-ink hover:border-teal hover:text-teal"
                onClick={() => { close(); actions.openInsert(a.section, a.index, "message"); }}>
                {b.message}
              </button>
              <button type="button" disabled={busy}
                className="rounded-full border border-teal px-2.5 py-1 text-xs font-medium text-teal hover:bg-teal hover:text-paper"
                onClick={() => { close(); actions.pickPauta(a.section, a.index); }}>
                {b.pauta}
              </button>
              <button type="button" className="text-xs text-muted hover:text-ink" onClick={close}>
                ✕
              </button>
            </div>
          )}
          {kind && (
            <div className="w-full max-w-md space-y-1">
              <input
                autoFocus
                value={text}
                onChange={(ev) => setText(ev.target.value)}
                onKeyDown={(ev) => { if (ev.key === "Enter" && kind !== "link") void insert(kind, text); if (ev.key === "Escape") setKind(""); }}
                placeholder={kind === "heading" ? b.headingPh : b.textPh}
                className="w-full rounded-[var(--radius-input)] border border-line bg-paper px-2 py-1 font-doc text-sm"
              />
              {kind === "link" && (
                <input
                  value={url}
                  onChange={(ev) => setUrl(ev.target.value)}
                  onKeyDown={(ev) => { if (ev.key === "Enter") void insert("link", text, url); if (ev.key === "Escape") setKind(""); }}
                  placeholder="https://"
                  className="w-full rounded-[var(--radius-input)] border border-line bg-paper px-2 py-1 text-xs"
                />
              )}
              {err && <p className="text-xs text-danger" role="alert">{err}</p>}
              <div className="flex gap-2">
                <Button size="sm" disabled={busy || !text.trim()}
                  onClick={() => void insert(kind, text, url)}>
                  {b.insert}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => { setKind(""); setErr(""); }}>
                  {t.doc.cancel}
                </Button>
              </div>
            </div>
          )}
        </div>
      )}
    </NodeViewWrapper>
  );
}

// --- node definitions ---

// IssueDoc is the issue's top node: the only things that may exist at
// document level are the issue's real parts — section/item heading
// atoms, sentence atoms, commentary blocks and the composer's layout
// blocks (KLOX-66). A paragraph can't wrap here, so a stray click+type
// next to a heading inserts nothing instead of a phantom paragraph
// that vanishes on reload.
export const IssueDoc = Node.create({
  name: "doc",
  topNode: true,
  content:
    "(sectionHeading | itemHeading | sentence | commentary | blockHeading | blockDivider | blockLink | blockImage | blockEmbed | blockText | blockAdd)+",
});

// blockNode builds one block node definition — shared shape: atom,
// (section, index) identity plus the block's content attrs.
function blockNode(name: string, extra: Record<string, unknown>, view: FC<NodeViewProps>) {
  return Node.create({
    name,
    group: "block",
    atom: true,
    selectable: true,
    addAttributes() {
      return {
        section: { default: "" },
        index: { default: 0 },
        count: { default: 0 },
        text: { default: "" },
        url: { default: "" },
        ...extra,
      };
    },
    parseHTML() {
      return [{ tag: `div[data-block="${name}"]` }];
    },
    renderHTML({ node, HTMLAttributes }) {
      return ["div", mergeAttributes(HTMLAttributes, { "data-block": name, "data-index": node.attrs.index }), 0];
    },
    addNodeView() {
      return ReactNodeViewRenderer(view);
    },
  });
}

export const BlockHeading = blockNode("blockHeading", {}, BlockHeadingView);
export const BlockDivider = blockNode("blockDivider", {}, BlockDividerView);
export const BlockLink = blockNode("blockLink", {}, BlockLinkView);
export const BlockImage = blockNode("blockImage", {}, BlockImageView);
export const BlockEmbed = blockNode("blockEmbed", {}, BlockEmbedView);
export const BlockAdd = blockNode("blockAdd", {}, BlockAddView);

// BlockText is the one content node among the layout blocks — real
// inline editing (bold/italic/link marks) inside paragraph content.
// The `md` attr is the stored-markdown baseline the autosave diffs.
export const BlockText = Node.create({
  name: "blockText",
  group: "block",
  content: "paragraph+",
  addAttributes() {
    return {
      section: { default: "" },
      index: { default: 0 },
      count: { default: 0 },
      md: { default: "" },
    };
  },
  parseHTML() {
    return [{ tag: `div[data-block="blockText"]` }];
  },
  renderHTML({ node, HTMLAttributes }) {
    return ["div", mergeAttributes(HTMLAttributes, { "data-block": "blockText", "data-index": node.attrs.index }), 0];
  },
  addNodeView() {
    return ReactNodeViewRenderer(BlockTextView);
  },
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
    return {
      ref: { default: 0 }, issueItemId: { default: 0 }, headline: { default: "" },
      empty: { default: false }, flagged: { default: 0 },
      section: { default: "" }, index: { default: 0 }, format: { default: "" },
      linkOnly: { default: false },
      // the item's citation sources — the inline composer renders its
      // [n] chips from them without a dossier fetch (KLOX-96)
      sources: { default: [] },
    };
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
