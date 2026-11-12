import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, type SampleDossier } from "../api";
import { useT } from "../i18n";
import { Badge, Button, Card } from "../ui";

// The landing page: product promise + a real cited dossier from the
// sample issue, fetched workspace-free so no demo session is created
// before the visitor chooses Start.
export function Landing() {
  const { t } = useT();
  const sample = useQuery({ queryKey: ["sample"], queryFn: api.sample });
  const pubs = useQuery({ queryKey: ["publications"], queryFn: api.publications });
  const first = pubs.data?.[0];

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-10">
      <p className="text-xs font-semibold uppercase tracking-widest text-teal">{t.landing.kicker}</p>
      <h1 className="mt-2 font-doc text-4xl font-semibold leading-tight">{t.landing.headline}</h1>
      <p className="mt-3 max-w-xl text-base text-muted">{t.landing.lede}</p>

      <div className="mt-6 flex flex-wrap items-center gap-3">
        <Link to="/new">
          <Button size="lg">{t.landing.start}</Button>
        </Link>
        {first && (
          <Link to={`/publications/${first.id}`} className="text-sm text-teal underline underline-offset-2">
            {t.landing.continueHint} {first.name} →
          </Link>
        )}
        {(pubs.data?.length ?? 0) > 1 && (
          <Link to="/publications" className="text-sm text-teal underline underline-offset-2">
            {t.landing.secondary} →
          </Link>
        )}
      </div>

      {sample.data && (
        <div className="mt-10">
          <p className="mb-2 text-xs uppercase tracking-wide text-muted">{t.landing.sampleCaption}</p>
          <div className="grid gap-4">
            {sample.data.dossiers.map((d) => (
              <SampleCard key={d.ref} d={d} />
            ))}
          </div>
        </div>
      )}

      {sample.data && sample.data.how_it_works.length > 0 && (
        <Card className="mt-8 p-5">
          <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted">
            {t.landing.howTitle}
          </h2>
          <ul className="grid gap-2 text-sm">
            {sample.data.how_it_works.map((h, i) => (
              <li key={i} className="flex gap-2">
                <span className="text-teal">•</span>
                <span>{h}</span>
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  );
}

function SampleCard({ d }: { t?: unknown; d: SampleDossier }) {
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
                href={`/sample/a/?u=${encodeURIComponent(src(f.ref)!.url)}`}
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
          <a key={s.ref} href={`/sample/a/?u=${encodeURIComponent(s.url)}`} target="_blank" rel="noreferrer">
            <Badge>{s.name || s.url}</Badge>
          </a>
        ))}
      </div>
    </Card>
  );
}
