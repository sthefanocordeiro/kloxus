import { Card, Badge } from "../ui";
import { useT } from "../i18n";
import type { SampleDossier } from "../api";

// sampleHref routes fictional .example sources through the demo article
// renderer; the recorded real sample links straight to the source.
export function sampleHref(url: string): string {
  return /^https?:\/\/[^/?#]*\.example([/?#]|$)/i.test(url) ? `/sample/a?u=${encodeURIComponent(url)}` : url;
}

export function SampleCard({ d }: { d: SampleDossier }) {
  const { t } = useT();
  const src = (ref: number) => d.sources.find((s) => s.ref === ref);
  return (
    <Card className="p-5">
      <h3 className="font-doc text-lg font-semibold leading-snug">{d.title}</h3>
      <p className="mt-1 text-sm text-muted">{d.summary}</p>

      <h4 className="mt-4 text-xs font-semibold uppercase tracking-wide text-muted">{t.landing.facts}</h4>
      <ul className="mt-1 grid gap-1.5 text-sm">
        {d.facts.map((f, i) => (
          <li key={i} className="flex items-baseline gap-2">
            <span className="min-w-0 flex-1">{f.text}</span>
            {src(f.ref) && (
              <a
                href={sampleHref(src(f.ref)!.url)}
                target="_blank"
                rel="noreferrer"
                title={src(f.ref)!.name || src(f.ref)!.url}
                className="shrink-0 rounded bg-teal/10 px-1.5 py-0.5 font-mono text-[11px] text-teal hover:bg-teal/20"
              >
                [{f.ref}]
              </a>
            )}
          </li>
        ))}
      </ul>

      {d.quote?.text && (
        <blockquote className="mt-4 border-l-2 border-teal/50 pl-3 text-sm italic text-muted">
          “{d.quote.text}”
          <span className="mt-1 block text-xs not-italic">— {d.quote.attribution}</span>
        </blockquote>
      )}

      {d.unknowns?.length > 0 && (
        <div className="mt-4">
          <h4 className="text-xs font-semibold uppercase tracking-wide text-muted">{t.landing.unknowns}</h4>
          <ul className="mt-1 grid gap-1 text-sm text-muted">
            {d.unknowns.map((u, i) => (
              <li key={i}>· {u}</li>
            ))}
          </ul>
        </div>
      )}

      <div className="mt-4 flex flex-wrap gap-1.5 border-t border-line pt-3">
        <span className="mr-1 text-xs uppercase tracking-wide text-muted">{t.landing.citedFrom}:</span>
        {d.sources.map((s) => (
          <a key={s.ref} href={sampleHref(s.url)} target="_blank" rel="noreferrer">
            <Badge>{s.name || s.url}</Badge>
          </a>
        ))}
      </div>
    </Card>
  );
}
