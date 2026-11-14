import { useContext, useEffect, useRef, useState } from "react";
import { PenLine } from "lucide-react";
import type { DocItem } from "../api";
import { DocActionsCtx } from "../editor/nodes";
import { useT } from "../i18n";
import { cn } from "../lib/cn";
import { Button } from "../ui";

// ComposeInsert is the evidence pane's way of pre-filling the box: a
// source excerpt plus that source's citation ref. seq lets the same
// excerpt insert twice.
export interface ComposeInsert {
  text: string;
  cite: number;
  seq: number;
}

// ComposeBox is the manual-writing surface for one lineup item: a
// textarea, one toggle chip per source (they become the [n] citations),
// an optional "my take" switch that stores an interpretation instead of
// a fact, and the add button. No model call lives behind any of it.
export function ComposeBox({ item, insert }: { item: DocItem; insert: ComposeInsert | null }) {
  const { t } = useT();
  const actions = useContext(DocActionsCtx);
  const [draft, setDraft] = useState("");
  const [cites, setCites] = useState<number[]>([]);
  const [myTake, setMyTake] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const area = useRef<HTMLTextAreaElement>(null);

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

  const empty = (item.sentences ?? []).length === 0;
  return (
    <section
      aria-label={t.compose.title}
      className="rounded-[var(--radius-card)] border border-dashed border-teal/50 bg-teal-soft/30 p-3"
    >
      <p className="mb-1 flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-teal">
        <PenLine size={12} /> {t.compose.title}
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
