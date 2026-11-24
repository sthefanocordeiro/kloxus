import { useWrite } from "../editor/writeStrings";
import { useQuery } from "@tanstack/react-query";
import { useContext, useEffect, useRef, useState } from "react";
import { ArrowRight, CheckCircle2, ChevronLeft, ChevronRight, Circle, ExternalLink, Plus } from "lucide-react";
import { api, type Check, type Coverage, type DocItem, type IssueDocument, type IssueItem } from "../api";
import { coverageDone, coverageLabel, readyFromChecks } from "../docModel";
import { DocActionsCtx } from "../editor/nodes";
import { sectionTitle, useT } from "../i18n";
import { useComposer } from "../editor/composerStrings";
import { cn } from "../lib/cn";
import { usePublishing } from "../lib/publishing";
import { fmtDate } from "../lib/fmt";
import { Markdown } from "../markdown";
import { Badge, Button, Card, SkeletonLine } from "../ui";

// --- left pane: the issue's pautas, grouped by section ---

export function PautasPane({
  doc,
  items,
  selectedRef,
  onSelect,
}: {
  doc: IssueDocument;
  items?: IssueItem[];
  selectedRef: number | null;
  onSelect: (ref: number, issueItemId: number) => void;
}) {
  const { t, lang } = useT();
  const b = useComposer();
  // no badge until the statuses are in — a fallback "a decidir" on an
  // approved pauta is the flicker the audit flagged (KLOX-100); a
  // missing item (stale list, fresh swap) reads as no badge, never the
  // wrong one
  const statusOf = (iid: number) => items?.find((i) => i.id === iid)?.status;
  const statusLabel = (s: string) => (t.itemStatus as Record<string, string>)[s] ?? s;
  return (
    <div className="space-y-4 p-3">
      {(doc.sections ?? []).map((sec) => (
        <div key={sec.title || "_"}>
          <p className="mb-1 px-1 text-[11px] font-semibold uppercase tracking-wide text-muted">
            {sec.title ? sectionTitle(lang, sec.title) : t.pautas}
          </p>
          <div className="space-y-1">
            {(sec.items ?? []).map((it) => {
              const st = statusOf(it.issue_item_id);
              return (
              <button
                key={it.ref}
                onClick={() => onSelect(it.ref, it.issue_item_id)}
                aria-label={`${it.headline} — ${statusLabel(st ?? "pending")}`}
                className={cn(
                  "w-full rounded-[var(--radius-card)] border px-3 py-2 text-left text-sm transition-colors",
                  selectedRef === it.ref ? "border-teal bg-teal-soft/50" : "border-line bg-paper-raised hover:border-ink/40",
                )}
              >
                <span className="font-doc leading-5 block">{it.headline}</span>
                <span className="mt-1 flex items-center gap-2">
                  {st && (
                    <Badge tone={st === "approved" ? "teal" : "muted"}>
                      {statusLabel(st)}
                    </Badge>
                  )}
                  {it.link_only && <Badge tone="muted">{b.linkOnly}</Badge>}
                  <span className="text-[10px] text-muted">
                    {sec.format === "links"
                      // a link-only pauta needs no text — "0 sentences"
                      // would read as missing work (KLOX-65/D2)
                      ? t.evidence.src((it.sources ?? []).length)
                      : `${t.evidence.sent((it.sentences ?? []).length)} · ${t.evidence.src((it.sources ?? []).length)}`}
                  </span>
                </span>
              </button>
              );
            })}
          </div>
        </div>
      ))}
    </div>
  );
}

// --- coverage counter, always visible ---

export function CoverageCounter({ coverage, onFixNow }: { coverage: Coverage; onFixNow: () => void }) {
  const { t } = useT();
  const done = coverageDone(coverage);
  // an empty document has nothing to cite — "0/0" reads as a failure
  // it isn't, so it shows a neutral label and no dead-end button
  // (KLOX-56)
  if (coverage.total === 0) {
    return (
      <div className="flex items-center gap-2">
        <Badge>{t.coverage.empty}</Badge>
      </div>
    );
  }
  return (
    <div className="flex items-center gap-2">
      <Badge tone={done ? "teal" : "warn"}>
        {coverageLabel(coverage)} {t.coverage.ok}
      </Badge>
      {!done && (
        <Button size="sm" variant="outline" onClick={onFixNow}>
          {t.coverage.fixNow} <ArrowRight size={12} />
        </Button>
      )}
    </div>
  );
}

// --- readiness checklist ---

// count === 1 labels — "1 pautas sem texto" read wrong (KLOX-124)
const SINGULAR_FAIL: Record<string, Record<string, string>> = {
  pt: {
    pending_decisions: "notícia por escolher",
    uncited_sentences: "frase que precisa de fonte",
    flags_unresolved: "pendência de fonte por resolver",
    empty_items: "notícia sem texto",
  },
  en: {
    pending_decisions: "story still undecided",
    uncited_sentences: "sentence that needs a source",
    flags_unresolved: "sentence still needing a source",
    empty_items: "story still empty",
  },
};

export function ReadinessList({
  checks,
  onTarget,
  heading = true,
}: {
  checks: Check[];
  onTarget: (c: Check) => void;
  // the card that hosts the list may already carry the title — the
  // review step did, printing "Antes de aprovar" twice (KLOX-68)
  heading?: boolean;
}) {
  const { t, lang } = useT();
  // every check is translated here; the server's message stays a
  // fallback for codes this UI doesn't know yet
  const labelFor = (c: Check): string => {
    const entry = (t.readiness.check as Record<string, { ok: string; fail: string }>)[c.code];
    let label = entry ? (c.ok ? entry.ok : entry.fail) : (c.message || c.code);
    if (!c.ok && c.count === 1 && SINGULAR_FAIL[lang]?.[c.code]) label = SINGULAR_FAIL[lang][c.code];
    if (!c.ok && c.count && c.count > 0 && !/^\d/.test(label)) label = `${c.count} ${label}`;
    if (!c.ok && c.target?.kind === "section") label += ` — ${sectionTitle(lang, c.target.ref)}`;
    return label;
  };
  return (
    <div>
      {heading && (
        <p className="mb-2 text-sm font-semibold">
          {readyFromChecks(checks) ? t.readiness.ready : t.readiness.title}
        </p>
      )}
      <ul className="space-y-0.5">
        {checks.map((c, i) => (
          <li key={i} className="flex items-center gap-2 text-sm">
            {c.ok ? (
              <CheckCircle2 size={15} className="text-teal shrink-0" />
            ) : (
              <Circle size={15} className="text-danger shrink-0" />
            )}
            <button
              disabled={c.ok || !c.target}
              onClick={() => onTarget(c)}
              className={cn(
                // checklist rows are jump targets — 20 px was under the
                // touch-target minimum (§4.3, KLOX-68)
                "inline-flex min-h-11 flex-1 items-center gap-1 text-left md:min-h-10",
                !c.ok && c.target && "text-danger underline decoration-dotted",
              )}
            >
              {labelFor(c)}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

// --- right pane: evidence for the selected item ---

// scrolls ONLY inside its own pane — scrollIntoView would scroll every
// scrollable ancestor and yank the document away from the clicked chip
export function scrollInPane(pane: HTMLElement, el: HTMLElement) {
  pane.scrollTop =
    el.getBoundingClientRect().top - pane.getBoundingClientRect().top +
    pane.scrollTop - pane.clientHeight / 2;
}

// The deterministic pipeline stores the triage rank note as the
// dossier's one-line "interpretation" — it is jargon, not editorial
// guidance, so the panel drops it (audit P0-3 / KLOX-96).
const RANK_NOTE = /^(nota de relevância|relevance score)\s*:/i;

export function EvidencePane({
  item,
  citeRef,
  sid,
  linkOnly,
  pos,
  onGo,
}: {
  item: DocItem | null;
  citeRef?: number;
  sid?: string;
  // link-only pauta (links-format section): the link is the source —
  // no compose or insert affordances (KLOX-65/D2)
  linkOnly?: boolean;
  // one-pauta-at-a-time (KLOX-96): the item's slot in the writable list
  // and a walker — the rail moves focus pauta by pauta
  pos?: { index: number; total: number };
  onGo?: (dir: -1 | 1) => void;
}) {
  const w = useWrite();
  const { t, lang } = useT();
  const b = useComposer();
  const actions = useContext(DocActionsCtx);
  const dossier = useQuery({
    queryKey: ["dossier", item?.issue_item_id],
    queryFn: () => api.dossier(item!.issue_item_id),
    enabled: !!item && item.issue_item_id !== 0,
  });
  const flash = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (citeRef && flash.current) {
      const el = flash.current.querySelector<HTMLElement>(`[data-evref="${citeRef}"]`);
      if (el) {
        scrollInPane(flash.current, el);
        el.classList.add("flash");
      }
    }
  }, [citeRef, dossier.data]);

  if (!item) {
    return <p className="p-4 text-sm text-muted">{t.evidence.empty}</p>;
  }
  const d = dossier.data?.dossier;
  const evFor = (ref: number) =>
    item.sentences
      .find((s) => s.id === sid)
      ?.evidence?.find((e) => e.ref === ref)?.excerpt;

  // An inserted excerpt is a quotation, not a fact claim: keep it inside
  // quotes, attribute it to the outlet, and cap it at 25 words so the
  // draft's originality rule (10+ consecutive source words) accepts it.
  // "+ inserir" on a source drops the pauta's FACT from that source as an
  // editable draft — no quotation marks, no outlet name (KLOX-124). With
  // no extracted fact it falls back to the opening of the excerpt, plain.
  const insertTextFor = (ref: number, raw: string) => {
    const fact = [...(d?.facts ?? []), ...(d?.deep_facts ?? [])].find((f) => f.ref === ref);
    if (fact) return fact.text;
    const words = raw.trim().split(/\s+/).filter(Boolean);
    return words.length > 25 ? words.slice(0, 25).join(" ") + "…" : words.join(" ");
  };

  // D2: a link-only pauta ships its story link with zero commentary —
  // no insert prompt, nothing pressuring the author to write. Sources
  // stay: the link is what ships. Link-only is the section format
  // (prop) or the item's own toggle (KLOX-66).
  const linkItem = !!linkOnly || !!item.link_only;
  // writing lives in the document — the rail's facts and excerpts feed
  // the inline composer through composeFor (KLOX-96)
  const canWrite = item.issue_item_id !== 0 && !linkItem && !actions.readOnly;
  const interpretation = (d?.interpretation ?? []).filter((s) => s.trim() !== "" && !RANK_NOTE.test(s));
  return (
    <div ref={flash} className="h-full space-y-4 overflow-y-auto p-3 text-sm">
      {pos && pos.total > 1 && (
        <div className="flex items-center justify-between text-xs text-muted">
          <button
            type="button"
            disabled={pos.index <= 0}
            onClick={() => onGo?.(-1)}
            aria-label={w.prevPauta}
            className="inline-flex items-center gap-0.5 rounded px-1 py-1 hover:text-ink disabled:opacity-40"
          >
            <ChevronLeft size={13} /> {w.prevPauta}
          </button>
          <span>{w.position(pos.index + 1, pos.total)}</span>
          <button
            type="button"
            disabled={pos.index >= pos.total - 1}
            onClick={() => onGo?.(1)}
            aria-label={w.nextPauta}
            className="inline-flex items-center gap-0.5 rounded px-1 py-1 hover:text-ink disabled:opacity-40"
          >
            {w.nextPauta} <ChevronRight size={13} />
          </button>
        </div>
      )}
      <p className="font-doc font-semibold leading-6">
        {item.headline}
        {linkItem && <Badge tone="muted" className="ml-2 align-middle">{b.linkOnly}</Badge>}
      </p>
      {d && (
        <>
          {d.facts.length + (d.deep_facts?.length ?? 0) > 0 && (
            <section>
              <p className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted">{t.evidence.facts}</p>
              <ul className="space-y-1">
                {[...d.facts, ...(d.deep_facts ?? [])].map((f, i) => (
                  <li key={i} className="text-[13px] leading-5">
                    {canWrite ? (
                      <button
                        type="button"
                        title={w.useFact}
                        aria-label={`${f.text} [${f.ref}]`}
                        onClick={() => actions.composeFor(item.ref, { text: f.text, cite: f.ref, seq: i + 1 })}
                        className="flex w-full items-start gap-1 rounded px-1.5 py-1 text-left transition-colors hover:bg-teal-soft"
                      >
                        <span className="flex-1">
                          {f.text} <span className="text-teal text-xs">[{f.ref}]</span>
                        </span>
                        <Plus size={12} className="mt-1 shrink-0 text-teal" />
                      </button>
                    ) : (
                      <span>
                        {f.text} <span className="text-teal text-xs">[{f.ref}]</span>
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </section>
          )}
          {interpretation.length > 0 && (
            <section>
              <p className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted">{t.evidence.interpretation}</p>
              <ul className="list-disc space-y-1 pl-5 text-[13px]">{interpretation.map((s, i) => <li key={i}>{s}</li>)}</ul>
            </section>
          )}
          {d.unknowns.length > 0 && (
            <section>
              <p className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted">{t.evidence.unknowns}</p>
              <ul className="list-disc space-y-1 pl-5 text-[13px]">{d.unknowns.map((s, i) => <li key={i}>{s}</li>)}</ul>
            </section>
          )}
        </>
      )}
      {item.sources.length > 0 && (
      <section>
        <p className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted">{t.evidence.sources}</p>
        <div className="space-y-2">
          {item.sources.map((s) => (
            <Card key={s.ref} data-evref={s.ref} className={cn("p-3", citeRef === s.ref && "ring-2 ring-teal")}>
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium">
                  <span className="mr-1 text-teal">[{s.ref}]</span>
                  {s.name}
                </span>
                {s.item_id !== 0 && (
                  <a href={`/a/${s.item_id}`} target="_blank" rel="noreferrer"
                    className="inline-flex items-center gap-1 text-xs text-teal hover:underline">
                    {t.evidence.openOriginal} <ExternalLink size={11} />
                  </a>
                )}
              </div>
              <p className="mt-1 text-[11px] text-muted">{s.date ? fmtDate(s.date, lang) : ""}</p>
              <p className="mt-1 border-l-2 border-line pl-2 text-[13px] leading-5 text-muted">
                {evFor(s.ref) ?? s.excerpt}
              </p>
              {canWrite && (
                <button
                  type="button"
                  onClick={() =>
                    actions.composeFor(item.ref, {
                      text: insertTextFor(s.ref, evFor(s.ref) ?? s.excerpt),
                      cite: s.ref, seq: s.ref,
                    })
                  }
                  title={t.compose.insertExcerpt(s.ref)}
                  aria-label={t.compose.insertExcerpt(s.ref)}
                  className="mt-1.5 inline-flex items-center gap-1 text-[11px] font-medium text-teal hover:underline"
                >
                  <Plus size={11} /> {t.compose.insert}
                </button>
              )}
            </Card>
          ))}
        </div>
      </section>
      )}
    </div>
  );
}

// --- preview pane: reader × surface switches ---

export function PreviewPane({ issueID }: { issueID: number }) {
  const { t } = useT();
  // KLOXUS_PUBLISHING off (KLOX-122): no reader/email/phone toggles - the
  // pane just shows the piece, full text.
  const publishing = usePublishing();
  const [pickedReader, setReader] = useState("non-subscriber");
  const [pickedSurface, setSurface] = useState("email");
  const reader = publishing ? pickedReader : "subscriber";
  const surface = publishing ? pickedSurface : "web";
  const pv = useQuery({
    queryKey: ["preview", issueID, reader, surface],
    queryFn: () => api.preview(issueID, reader, surface),
  });
  const readers = ["non-subscriber", "subscriber"] as const;
  const surfaces = ["email", "web", "phone"] as const;
  const names: Record<string, string> = {
    "non-subscriber": t.preview.nonSubscriber,
    subscriber: t.preview.subscriber,
    email: t.preview.email,
    web: t.preview.web,
    phone: t.preview.phone,
  };
  return (
    <div className="flex h-full flex-col">
      {publishing && <div className="flex flex-wrap gap-3 border-b border-line p-3">
        {(
          [
            [t.preview.reader, readers, reader, setReader],
            [t.preview.surface, surfaces, surface, setSurface],
          ] as const
        ).map(([label, opts, val, set]) => (
          <div key={label} className="flex items-center gap-1 text-xs">
            <span className="text-muted">{label}</span>
            {opts.map((o) => (
              <button
                key={o}
                onClick={() => (set as (v: string) => void)(o)}
                className={cn(
                  // 24 px toggle chips were under the touch-target
                  // minimum (§4.3, KLOX-68)
                  "inline-flex min-h-11 items-center rounded px-2.5 md:min-h-10",
                  val === o ? "bg-ink text-paper" : "bg-line/50 text-muted",
                )}
              >
                {names[o]}
              </button>
            ))}
          </div>
        ))}
      </div>}
      <div className={cn("flex-1 overflow-y-auto p-4", surface === "phone" && "mx-auto w-[360px]")}>
        {pv.data ? <Markdown md={pv.data.body_md} /> : <SkeletonLine className="w-64" />}
      </div>
    </div>
  );
}
