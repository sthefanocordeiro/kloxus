import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { api, type SampleDossier } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Badge, Button, Card } from "../ui";

// Sponsors and technology named on the landing. `status` is the only
// switch: flip an entry to "live" when its integrating issue merges —
// the page must never claim a feature is live before then. Descriptions
// live in the i18n dict under landing.stackDesc.
const STACK = [
  { id: "firecrawl", name: "Firecrawl", status: "live" },
  { id: "resend", name: "Resend", status: "coming" },
  { id: "supabase", name: "Supabase", status: "coming" },
  { id: "abacatepay", name: "AbacatePay", status: "coming" },
] as const;

// The landing page: product promise, the loop, a real cited dossier from
// the sample issue (fetched workspace-free), the rules, the stack and a
// short FAQ. Start resolves-or-creates the visitor's workspace through
// the signed-cookie flow (D17), then lands on onboarding.
export function Landing() {
  const { t } = useT();
  const nav = useNavigate();
  const sample = useQuery({ queryKey: ["sample"], queryFn: api.sample });
  const pubs = useQuery({ queryKey: ["publications"], queryFn: api.publications });
  const first = pubs.data?.[0];

  const start = useMutation({
    mutationFn: api.workspace,
    onSuccess: () => nav("/new"),
  });

  const cta = (
    <div>
      <Button size="lg" onClick={() => start.mutate()} disabled={start.isPending}>
        {start.isPending ? t.landing.starting : t.landing.start}
      </Button>
      {start.error && (
        <p role="alert" className="mt-2 text-sm text-danger">
          {errText(t, start.error)}
        </p>
      )}
    </div>
  );

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-10">
      <p className="text-xs font-semibold uppercase tracking-widest text-teal">{t.landing.kicker}</p>
      <h1 className="mt-2 font-doc text-4xl font-semibold leading-tight">{t.landing.headline}</h1>
      <p className="mt-3 max-w-xl text-base text-muted">{t.landing.lede}</p>
      <p className="mt-2 max-w-xl text-sm text-muted">{t.landing.forWhom}</p>

      <div className="mt-6 flex flex-wrap items-center gap-x-4 gap-y-3">
        {cta}
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
      <p className="mt-2 text-xs text-muted">{t.landing.startHint}</p>

      <section aria-labelledby="landing-how" className="mt-12">
        <h2 id="landing-how" className="font-doc text-2xl font-semibold">
          {t.landing.howTitle}
        </h2>
        <ol className="mt-4 grid list-decimal gap-2.5 pl-5 text-sm marker:font-semibold marker:text-teal">
          {t.landing.howSteps.map((s, i) => (
            <li key={i} className="pl-1">
              {s}
            </li>
          ))}
        </ol>
      </section>

      {sample.data && sample.data.dossiers.length > 0 && (
        <section aria-labelledby="landing-sample" className="mt-12">
          <h2 id="landing-sample" className="font-doc text-2xl font-semibold">
            {t.landing.sampleTitle}
          </h2>
          <p className="mt-1 text-xs uppercase tracking-wide text-muted">{t.landing.sampleCaption}</p>
          <div className="mt-4 grid gap-4">
            {sample.data.dossiers.map((d) => (
              <SampleCard key={d.ref} d={d} />
            ))}
          </div>
          <p className="mt-3 text-xs text-muted">{t.landing.sampleNote}</p>
        </section>
      )}

      <section aria-labelledby="landing-rules" className="mt-12">
        <h2 id="landing-rules" className="font-doc text-2xl font-semibold">
          {t.landing.rulesTitle}
        </h2>
        <div className="mt-4 grid gap-4 sm:grid-cols-3">
          {t.landing.rules.map((r, i) => (
            <Card key={i} className="p-4">
              <h3 className="text-sm font-semibold">{r.title}</h3>
              <p className="mt-1.5 text-sm text-muted">{r.body}</p>
            </Card>
          ))}
        </div>
      </section>

      <section aria-labelledby="landing-sources" className="mt-12">
        <h2 id="landing-sources" className="font-doc text-2xl font-semibold">
          {t.landing.sourcesTitle}
        </h2>
        <ul className="mt-4 grid gap-2 text-sm">
          {t.landing.sourcesPoints.map((p, i) => (
            <li key={i} className="flex gap-2">
              <span className="text-teal">•</span>
              <span>{p}</span>
            </li>
          ))}
        </ul>
      </section>

      <section aria-labelledby="landing-stack" className="mt-12">
        <h2 id="landing-stack" className="font-doc text-2xl font-semibold">
          {t.landing.stackTitle}
        </h2>
        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          {STACK.map((s) => (
            <Card key={s.id} className="p-4">
              <div className="flex items-center gap-2">
                <h3 className="text-sm font-semibold">{s.name}</h3>
                <Badge tone={s.status === "live" ? "teal" : "muted"}>
                  {t.landing.stackStatus[s.status]}
                </Badge>
              </div>
              <p className="mt-1.5 text-sm text-muted">{t.landing.stackDesc[s.id]}</p>
            </Card>
          ))}
        </div>
        <p className="mt-3 text-xs text-muted">{t.landing.stackNote}</p>
      </section>

      <section aria-labelledby="landing-faq" className="mt-12">
        <h2 id="landing-faq" className="font-doc text-2xl font-semibold">
          {t.landing.faqTitle}
        </h2>
        <div className="mt-2">
          {t.landing.faq.map((f, i) => (
            <details key={i} className="border-b border-line py-3">
              <summary className="cursor-pointer text-sm font-medium">{f.q}</summary>
              <p className="mt-2 text-sm text-muted">{f.a}</p>
            </details>
          ))}
        </div>
      </section>

      <section aria-labelledby="landing-final" className="mt-12 border-t border-line pt-10">
        <h2 id="landing-final" className="font-doc text-2xl font-semibold">
          {t.landing.finalTitle}
        </h2>
        <p className="mt-2 max-w-xl text-sm text-muted">{t.landing.finalBody}</p>
        <div className="mt-5">{cta}</div>
      </section>
    </div>
  );
}

function SampleCard({ d }: { d: SampleDossier }) {
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
                href={`/sample/a?u=${encodeURIComponent(src(f.ref)!.url)}`}
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
          <a key={s.ref} href={`/sample/a?u=${encodeURIComponent(s.url)}`} target="_blank" rel="noreferrer">
            <Badge>{s.name || s.url}</Badge>
          </a>
        ))}
      </div>
    </Card>
  );
}
