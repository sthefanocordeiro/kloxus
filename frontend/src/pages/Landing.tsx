import { BookOpen, ClipboardCopy, PenLine } from "lucide-react";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Card } from "../ui";
import { SiteCta, SiteFooter, SiteHeader, useSiteEntry } from "../components/SiteChrome";

// The landing page (KLOX-61 §1, reworked per owner review KLOX-69):
// sticky header with the PT/EN switch and a hamburger menu on mobile,
// a dark-green hero with a simplified product mock (legible at both
// widths — the old full-app screenshot read as decoration at 390 px),
// a real-sources strip, three numbered steps, three cards for writers,
// a collapsed FAQ, a closing band and the shared site footer. Start
// resolves-or-creates the visitor's workspace through the
// signed-cookie flow (D17) and lands on onboarding; a returning
// visitor gets "Ir para o painel" instead.
export function Landing() {
  const { t } = useT();
  const entry = useSiteEntry();

  const cardIcons = [PenLine, BookOpen, ClipboardCopy];

  return (
    <div className="min-h-dvh bg-paper text-ink">
      <SiteHeader />

      <section className="bg-teal text-paper">
        <div className="mx-auto grid max-w-6xl gap-10 px-4 py-14 lg:grid-cols-[1.05fr_1fr] lg:items-center lg:py-20">
          <div>
            <h1 className="font-doc text-3xl font-bold leading-tight md:text-5xl">
              {t.landing.headline}
            </h1>
            <p className="mt-4 max-w-xl text-lg text-paper/80">{t.landing.subhead}</p>
            <div className="mt-7">
              <SiteCta size="lg" entry={entry} />
            </div>
            <p className="mt-4 text-sm text-paper/80">{t.landing.tryLine}</p>
            {entry.start.error && (
              <p role="alert" className="mt-2 text-sm text-danger-soft">
                {errText(t, entry.start.error)}
              </p>
            )}
          </div>
          <HeroMock />
        </div>
      </section>

      <section aria-label={t.landing.sourcesStrip} className="border-b border-line bg-paper">
        <div className="mx-auto flex max-w-6xl flex-wrap items-baseline gap-x-6 gap-y-1.5 px-4 py-5">
          <p className="shrink-0 text-xs font-semibold uppercase tracking-wider text-muted">
            {t.landing.sourcesStrip}
          </p>
          <ul className="flex flex-wrap items-baseline gap-x-5 gap-y-1">
            {SOURCES.map((n) => (
              <li key={n} className="font-doc text-[15px] font-semibold text-ink/80">
                {n}
              </li>
            ))}
          </ul>
        </div>
      </section>

      <section id="como-funciona" aria-labelledby="landing-how" className="scroll-mt-20 border-b border-line">
        <div className="mx-auto max-w-6xl px-4 py-14 md:py-16">
          <h2 id="landing-how" className="font-doc text-2xl font-bold md:text-3xl">
            {t.landing.howTitle}
          </h2>
          <ol className="mt-8 grid gap-6 md:grid-cols-3">
            {t.landing.howSteps.map((s, i) => (
              <li key={i} className="flex gap-3">
                <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-teal text-sm font-bold text-paper">
                  {i + 1}
                </span>
                <div>
                  <h3 className="font-display text-lg font-bold">{s.title}</h3>
                  <p className="mt-1 text-sm text-muted">{s.body}</p>
                </div>
              </li>
            ))}
          </ol>
        </div>
      </section>

      <section aria-labelledby="landing-writers" className="bg-surface">
        <div className="mx-auto max-w-6xl px-4 py-14 md:py-16">
          <h2 id="landing-writers" className="font-doc text-2xl font-bold md:text-3xl">
            {t.landing.cardsTitle}
          </h2>
          <div className="mt-8 grid gap-5 md:grid-cols-3">
            {t.landing.cards.map((c, i) => {
              const Icon = cardIcons[i];
              return (
                <Card key={i} className="p-5">
                  <span className="flex h-10 w-10 items-center justify-center rounded-lg bg-teal-soft text-teal">
                    <Icon size={20} aria-hidden />
                  </span>
                  <h3 className="mt-4 font-display text-lg font-bold">{c.title}</h3>
                  <p className="mt-1.5 text-sm text-muted">{c.body}</p>
                </Card>
              );
            })}
          </div>
        </div>
      </section>

      <section id="perguntas" aria-labelledby="landing-faq" className="scroll-mt-20 border-t border-line">
        <div className="mx-auto max-w-3xl px-4 py-14 md:py-16">
          <h2 id="landing-faq" className="font-doc text-2xl font-bold md:text-3xl">
            {t.landing.faqTitle}
          </h2>
          <div className="mt-4">
            {t.landing.faq.map((f, i) => (
              <details key={i} className="border-b border-line py-4">
                <summary className="cursor-pointer text-base font-medium">{f.q}</summary>
                <p className="mt-2 text-sm text-muted">{f.a}</p>
              </details>
            ))}
          </div>
        </div>
      </section>

      <section aria-labelledby="landing-close" className="bg-teal text-paper">
        <div className="mx-auto max-w-6xl px-4 py-14 text-center md:py-16">
          <h2 id="landing-close" className="font-doc text-2xl font-bold md:text-3xl">
            {t.landing.closingTitle}
          </h2>
          <div className="mt-6 flex justify-center">
            <SiteCta size="lg" entry={entry} />
          </div>
        </div>
      </section>

      <SiteFooter />
    </div>
  );
}

// The "Fontes reais" credibility strip (KLOX-69): real outlet names,
// same in both locales — proper nouns need no translation.
const SOURCES = [
  "Tecnoblog",
  "Olhar Digital",
  "TabNews",
  "MacMagazine",
  "Meio Bit",
  "Startups.com.br",
];

// A simplified product illustration inside a clean browser frame
// (KLOX-69 #2): the pauta rail plus the writing surface, rendered as
// real text so it stays legible at 1440 px and meaningful at 390 px —
// on the smallest widths the rail drops out and the document carries
// the picture alone.
function HeroMock() {
  const { t } = useT();
  const m = t.landing.mock;
  return (
    <div
      role="img"
      aria-label={t.landing.shotAlt}
      className="overflow-hidden rounded-xl border border-white/15 bg-paper text-ink shadow-2xl"
    >
      <div className="flex h-9 items-center gap-1.5 border-b border-line bg-surface px-3">
        <span className="h-2.5 w-2.5 rounded-full bg-line" />
        <span className="h-2.5 w-2.5 rounded-full bg-line" />
        <span className="h-2.5 w-2.5 rounded-full bg-line" />
        <span className="mx-auto truncate rounded-md bg-paper px-6 py-0.5 text-[11px] text-muted">
          kloxus.fly.dev
        </span>
      </div>
      <div className="grid sm:grid-cols-[2fr_3fr]">
        <div className="hidden border-r border-line bg-surface/70 p-3 sm:block">
          <p className="text-[11px] font-semibold uppercase tracking-wider text-muted">
            {m.railTitle}
          </p>
          <ul className="mt-2 grid gap-2">
            {m.items.map((title, i) => (
              <li key={i} className="rounded-lg border border-line bg-paper p-2.5">
                <p className="text-[12.5px] font-medium leading-snug">{title}</p>
                <span className="mt-1.5 inline-block rounded-md bg-teal-soft px-1.5 py-0.5 text-[10px] font-semibold text-chip">
                  {t.itemStatus.approved}
                </span>
              </li>
            ))}
          </ul>
        </div>
        <div className="p-4 sm:p-5">
          <p className="text-[11px] font-semibold uppercase tracking-wider text-muted">
            {m.editorTitle}
          </p>
          <p className="mt-1 font-doc text-xl font-semibold leading-snug">{m.docTitle}</p>
          <div className="mt-3 grid gap-2" aria-hidden="true">
            <div className="h-2.5 w-11/12 rounded bg-line" />
            <div className="h-2.5 w-full rounded bg-line" />
            <div className="h-2.5 w-3/5 rounded bg-line" />
          </div>
          <div className="mt-4 rounded-lg border border-teal/20 bg-teal-soft/60 p-2.5">
            <p className="text-[11px] font-semibold uppercase tracking-wider text-chip">
              {t.commentary.label}
            </p>
            <div className="mt-1.5 h-2.5 w-4/5 rounded bg-teal/15" aria-hidden="true" />
          </div>
          <div className="mt-4 flex items-center gap-2" aria-hidden="true">
            <span className="rounded-md bg-line/70 px-1.5 py-0.5 text-[10px] font-semibold text-muted">
              [1]
            </span>
            <div className="h-2 w-2/5 rounded bg-line" />
          </div>
        </div>
      </div>
    </div>
  );
}
