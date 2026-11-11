import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { ArrowRight, CheckCircle2, Circle, ExternalLink } from "lucide-react";
import { api, type Check, type Coverage, type DocItem, type IssueDocument, type IssueItem } from "../api";
import { coverageDone, coverageLabel, readyFromChecks } from "../docModel";
import { useT } from "../i18n";
import { cn } from "../lib/cn";
import { Markdown } from "../markdown";
import { Badge, Button, Card } from "../ui";

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
  const { t } = useT();
  const statusOf = (iid: number) => items?.find((i) => i.id === iid)?.status ?? "";
  return (
    <div className="space-y-4 p-3">
      {doc.sections.map((sec) => (
        <div key={sec.title || "_"}>
          <p className="mb-1 px-1 text-[11px] font-semibold uppercase tracking-wide text-muted">
            {sec.title || t.pautas}
          </p>
          <div className="space-y-1">
            {sec.items.map((it) => (
              <button
                key={it.ref}
                onClick={() => onSelect(it.ref, it.issue_item_id)}
                className={cn(
                  "w-full rounded-[var(--radius-card)] border px-3 py-2 text-left text-sm transition-colors",
                  selectedRef === it.ref ? "border-teal bg-teal-soft/50" : "border-line bg-paper-raised hover:border-ink/40",
                )}
              >
                <span className="font-doc leading-5 block">{it.headline}</span>
                <span className="mt-1 flex items-center gap-2">
                  <Badge tone={statusOf(it.issue_item_id) === "approved" ? "teal" : "muted"}>
                    {statusOf(it.issue_item_id) || "—"}
                  </Badge>
                  <span className="text-[10px] text-muted">{it.sentences.length} ·</span>
                  <span className="text-[10px] text-muted">{it.sources.length} {t.evidence.sources.toLowerCase()}</span>
                </span>
              </button>
            ))}
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

export function ReadinessList({
  checks,
  coverage,
  onTarget,
}: {
  checks: Check[];
  coverage: Coverage;
  onTarget: (c: Check) => void;
}) {
  const { t } = useT();
  const labels = t.readiness as Record<string, string>;
  return (
    <div>
      <p className="mb-2 text-sm font-semibold">
        {readyFromChecks(checks) ? t.readiness.ready : t.readiness.title}
      </p>
      <ul className="space-y-1.5">
        {checks.map((c, i) => (
          <li key={i} className="flex items-start gap-2 text-sm">
            {c.ok ? (
              <CheckCircle2 size={15} className="mt-0.5 text-teal shrink-0" />
            ) : (
              <Circle size={15} className="mt-0.5 text-danger shrink-0" />
            )}
            <button
              disabled={c.ok || !c.target}
              onClick={() => onTarget(c)}
              className={cn("text-left", !c.ok && c.target && "text-danger underline decoration-dotted")}
            >
              {c.message || labels[c.code] || c.code}
            </button>
          </li>
        ))}
      </ul>
      <div className="mt-3">
        <CoverageCounter coverage={coverage} onFixNow={() => onTarget({ code: "fix", ok: false })} />
      </div>
    </div>
  );
}

// --- right pane: evidence for the selected item ---

export function EvidencePane({
  item,
  citeRef,
  sid,
}: {
  item: DocItem | null;
  citeRef?: number;
  sid?: string;
}) {
  const { t } = useT();
  const dossier = useQuery({
    queryKey: ["dossier", item?.issue_item_id],
    queryFn: () => api.dossier(item!.issue_item_id),
    enabled: !!item && item.issue_item_id !== 0,
  });
  const flash = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (citeRef && flash.current) {
      const el = flash.current.querySelector(`[data-evref="${citeRef}"]`);
      el?.scrollIntoView({ block: "center" });
      el?.classList.add("flash");
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

  return (
    <div ref={flash} className="space-y-4 p-3 text-sm">
      <p className="font-doc font-semibold leading-6">{item.headline}</p>
      {d && (
        <>
          {d.facts.length + (d.deep_facts?.length ?? 0) > 0 && (
            <section>
              <p className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted">{t.evidence.facts}</p>
              <ul className="space-y-1">
                {[...d.facts, ...(d.deep_facts ?? [])].map((f, i) => (
                  <li key={i} className="text-[13px] leading-5">
                    {f.text} <span className="text-teal text-xs">[{f.ref}]</span>
                  </li>
                ))}
              </ul>
            </section>
          )}
          {d.interpretation.length > 0 && (
            <section>
              <p className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted">{t.evidence.interpretation}</p>
              <ul className="list-disc space-y-1 pl-5 text-[13px]">{d.interpretation.map((s, i) => <li key={i}>{s}</li>)}</ul>
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
                  <a href={`/item/${s.item_id}`} target="_blank" rel="noreferrer"
                    className="inline-flex items-center gap-1 text-xs text-teal hover:underline">
                    {t.evidence.openOriginal} <ExternalLink size={11} />
                  </a>
                )}
              </div>
              <p className="mt-1 text-[11px] text-muted">{s.date}</p>
              <p className="mt-1 border-l-2 border-line pl-2 text-[13px] leading-5 text-muted">
                {evFor(s.ref) ?? s.excerpt}
              </p>
            </Card>
          ))}
        </div>
      </section>
    </div>
  );
}

// --- preview pane: reader × surface switches ---

export function PreviewPane({ issueID }: { issueID: number }) {
  const { t } = useT();
  const [reader, setReader] = useState("non-subscriber");
  const [surface, setSurface] = useState("email");
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
      <div className="flex flex-wrap gap-3 border-b border-line p-3">
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
                className={cn("rounded px-2 py-1", val === o ? "bg-ink text-paper" : "bg-line/50 text-muted")}
              >
                {names[o]}
              </button>
            ))}
          </div>
        ))}
      </div>
      <div className={cn("flex-1 overflow-y-auto p-4", surface === "phone" && "mx-auto w-[360px]")}>
        {pv.data ? <Markdown md={pv.data.body_md} /> : <p className="text-sm text-muted">…</p>}
      </div>
    </div>
  );
}
