import { useContext, useEffect, useRef, useState } from "react";
import { PenLine, X } from "lucide-react";
import { DocActionsCtx, type ComposeInsert } from "../editor/actions";
import { useT } from "../i18n";
import { cn } from "../lib/cn";
import { Button } from "../ui";

// the action contract moved to editor/actions (KLOX-96) — re-exported
// here so existing imports keep working
export type { ComposeInsert };

// ComposeItem is the slice of a document item the box needs: its ref
// and citation sources. A DocItem satisfies it, and so does the trimmed
// shape an item heading carries in its attrs.
export interface ComposeItem {
  ref: number;
  sources: { ref: number; name: string }[];
  sentences?: { id: string }[];
}

// ComposeBox is the manual-writing surface for one lineup item: a
// textarea, one toggle chip per source (they become the [n] citations),
// an optional "my take" switch that stores an interpretation instead of
// a fact, and the add button. No model call lives behind any of it.
// It renders inside the document, under the pauta's heading (KLOX-96).
export function ComposeBox({
  item,
  empty: emptyProp,
  insert,
  onClose,
}: {
  item: ComposeItem;
  // overrides the empty-from-sentences default — an item heading knows
  // the item is empty without carrying its sentence list
  empty?: boolean;
  insert: ComposeInsert | null;
  onClose?: () => void;
}) {
  const { t } = useT();
  const actions = useContext(DocActionsCtx);
  // the document rebuilds on every save — this view unmounts with it.
  // The page parks the in-progress input (composeDraftTake/Save) so a
  // remount mid-sentence loses nothing; the take consumes it once so
  // the parked copy can't resurrect over newer input (KLOX-96)
  const [parked] = useState(() => actions.composeDraftTake(item.ref));
  const [draft, setDraft] = useState(parked?.text ?? "");
  const [cites, setCites] = useState<number[]>(parked?.cites ?? []);
  const [myTake, setMyTake] = useState(parked?.myTake ?? false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const area = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    actions.composeDraftSave(item.ref, { text: draft, cites, myTake });
  }, [draft, cites, myTake, item.ref, actions]);

  useEffect(() => {
    if (!insert) return;
    setDraft((d) => (d ? d + " " : "") + insert.text);
    setCites((c) => (c.includes(insert.cite) ? c : [...c, insert.cite].sort((a, b) => a - b)));
    setMyTake(false);
    area.current?.focus();
  }, [insert]);

  const toggleCite = (n: number) =>
    setCites((c) => (c.includes(n) ? c.filter((x) => x !== n) : [...c, n].sort((a, b) => a - b)));

  const add = async () => {
    const text = draft.trim();
    if (!text || busy) return;
    setBusy(true);
    setErr("");
    const refusal = await actions.addSentence(
      item.ref, text, myTake ? [] : cites, myTake ? "interpretation" : "fact",
    );
    setBusy(false);
    if (refusal === null) {
      setDraft("");
      setCites([]);
      setMyTake(false);
    } else {
      setErr(refusal);
    }
  };

  const empty = emptyProp ?? (item.sentences ?? []).length === 0;
  return (
    <section
      aria-label={t.compose.title}
      className="rounded-[var(--radius-card)] border border-dashed border-teal/50 bg-teal-soft/30 p-3"
    >
      <p className="mb-1 flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-teal">
        <PenLine size={12} /> {t.compose.title}
        {onClose && (
          <button
            type="button"
            onClick={onClose}
            aria-label={t.doc.cancel}
            className="ml-auto rounded p-0.5 text-muted hover:text-ink"
          >
            <X size={13} />
          </button>
        )}
      </p>
      {empty && <p className="mb-2 text-xs text-muted">{t.compose.emptyHint}</p>}
      <textarea
        ref={area}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        rows={3}
        placeholder={t.compose.placeholder}
        aria-label={t.compose.title}
        className="w-full resize-y rounded-[var(--radius-input)] border border-line bg-paper p-2 font-doc text-sm leading-6"
      />
      {!myTake && item.sources.length > 0 && (
        <div className="mt-1.5 flex flex-wrap gap-1" role="group" aria-label={t.compose.citeLabel}>
          {item.sources.map((s) => (
            <button
              key={s.ref}
              type="button"
              onClick={() => toggleCite(s.ref)}
              aria-pressed={cites.includes(s.ref)}
              title={s.name}
              className={cn(
                "rounded px-1.5 py-0.5 text-[11px] font-medium",
                cites.includes(s.ref) ? "bg-teal text-paper" : "bg-line/60 text-muted hover:bg-line",
              )}
            >
              [{s.ref}] {s.name}
            </button>
          ))}
        </div>
      )}
      <label className="mt-2 flex cursor-pointer items-center gap-1.5 text-xs text-muted">
        <input type="checkbox" checked={myTake} onChange={(e) => setMyTake(e.target.checked)} />
        {t.compose.interpretation}
      </label>
      {err && (
        <p className="mt-1.5 text-xs text-danger" role="alert">
          {err}
        </p>
      )}
      <div className="mt-2">
        <Button size="sm" disabled={busy || !draft.trim()} onClick={add}>
          {t.compose.add}
        </Button>
      </div>
    </section>
  );
}
