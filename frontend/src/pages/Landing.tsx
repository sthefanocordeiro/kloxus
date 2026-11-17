import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { BookOpen, PenLine, Wallet } from "lucide-react";
import { api } from "../api";
import { useT, type Lang } from "../i18n";
import { cn } from "../lib/cn";
import { errText } from "../lib/errors";
import { Button, Card } from "../ui";
import heroShot from "../assets/editor-hero.jpg";

// The landing page (KLOX-61 §1): sticky header, a dark-green hero with
// a static picture of the editor, three numbered steps, three cards
// for writers, a collapsed FAQ, a closing band and a full footer that
// carries the language switch. Start resolves-or-creates the visitor's
// workspace through the signed-cookie flow (D17) and lands on
// onboarding; a returning visitor gets "Ir para o painel" instead.
export function Landing() {
  const { t, lang, setLang } = useT();
  const nav = useNavigate();
  const pubs = useQuery({ queryKey: ["publications"], queryFn: api.publications, retry: false });
  const returning = (pubs.data?.length ?? 0) > 0;

  const start = useMutation({ mutationFn: api.workspace });
  const create = () => start.mutate(undefined, { onSuccess: () => nav("/new") });
  const signIn = () => start.mutate(undefined, { onSuccess: () => nav("/publications") });

  const cardIcons = [PenLine, BookOpen, Wallet];

  const cta = (size: "sm" | "lg") => (
    <Button size={size} variant="accent" onClick={create} disabled={start.isPending}>
      {start.isPending ? t.landing.starting : t.landing.cta}
    </Button>
  );

  return (
    <div className="min-h-dvh bg-paper text-ink">
      <header className="sticky top-0 z-40 border-b border-line bg-paper/90 backdrop-blur">
        <div className="mx-auto flex h-16 max-w-6xl items-center gap-5 px-4">
          <Link to="/" className="font-display text-lg font-extrabold tracking-tight text-teal">
            {t.appName}
          </Link>
          <nav className="hidden items-center gap-5 text-sm text-muted md:flex">
            <a href="#como-funciona" className="hover:text-ink">
              {t.landing.howTitle}
            </a>
            <a href="#perguntas" className="hover:text-ink">
              {t.landing.faqTitle}
            </a>
          </nav>
          <div className="ml-auto flex items-center gap-1">
            {returning ? (
              <Link
                to="/publications"
                className="inline-flex h-11 items-center rounded-[var(--radius-btn)] px-3 text-sm font-medium text-ink hover:bg-surface md:h-10"
              >
                {t.landing.navDashboard}
              </Link>
            ) : (
              <button
                onClick={signIn}
                disabled={start.isPending}
                className="inline-flex h-11 cursor-pointer items-center rounded-[var(--radius-btn)] px-3 text-sm font-medium text-ink hover:bg-surface disabled:opacity-50 md:h-10"
              >
                {t.landing.navSignIn}
              </button>
            )}
            {cta("sm")}
          </div>
        </div>
      </header>

      <section className="bg-teal text-paper">
        <div className="mx-auto grid max-w-6xl gap-10 px-4 py-14 lg:grid-cols-[1.05fr_1fr] lg:items-center lg:py-20">
          <div>
            <h1 className="font-display text-3xl font-extrabold leading-tight md:text-5xl">
              {t.landing.headline}
            </h1>
            <p className="mt-4 max-w-xl text-lg text-paper/80">{t.landing.subhead}</p>
            <div className="mt-7">{cta("lg")}</div>
            <p className="mt-3 text-sm text-paper/70">{t.landing.tryLine}</p>
            {start.error && (
              <p role="alert" className="mt-2 text-sm text-danger-soft">
                {errText(t, start.error)}
              </p>
            )}
          </div>
          <div className="overflow-hidden rounded-xl border border-white/15 bg-paper shadow-2xl">
            <div className="flex h-9 items-center gap-1.5 border-b border-line bg-surface px-3">
              <span className="h-2.5 w-2.5 rounded-full bg-line" />
              <span className="h-2.5 w-2.5 rounded-full bg-line" />
              <span className="h-2.5 w-2.5 rounded-full bg-line" />
              <span className="mx-auto truncate rounded-md bg-paper px-6 py-0.5 text-[11px] text-muted">
                kloxus.fly.dev
              </span>
            </div>
            <img src={heroShot} alt={t.landing.shotAlt} className="block w-full" />
          </div>
        </div>
      </section>

      <section id="como-funciona" aria-labelledby="landing-how" className="scroll-mt-20 border-b border-line">
        <div className="mx-auto max-w-6xl px-4 py-14 md:py-16">
          <h2 id="landing-how" className="font-display text-2xl font-bold md:text-3xl">
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
          <h2 id="landing-writers" className="font-display text-2xl font-bold md:text-3xl">
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
          <h2 id="landing-faq" className="font-display text-2xl font-bold md:text-3xl">
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
          <h2 id="landing-close" className="font-display text-2xl font-bold md:text-3xl">
            {t.landing.closingTitle}
          </h2>
          <div className="mt-6 flex justify-center">{cta("lg")}</div>
        </div>
      </section>

      <footer className="border-t border-line bg-paper">
        <div className="mx-auto grid max-w-6xl gap-8 px-4 py-12 sm:grid-cols-2 md:grid-cols-[1.5fr_1fr_1fr_1.5fr]">
          <div>
            <p className="font-display text-lg font-extrabold tracking-tight text-teal">
              {t.appName}
            </p>
            <div className="mt-4 flex items-center gap-1" aria-label={t.nav.language}>
              {(["en", "pt"] as Lang[]).map((l) => (
                <button
                  key={l}
                  onClick={() => setLang(l)}
                  className={cn(
                    "flex h-11 min-w-11 items-center justify-center rounded-md px-1.5 text-xs md:h-10 md:min-w-10",
                    lang === l ? "bg-ink font-medium text-paper" : "text-muted hover:text-ink",
                  )}
                  aria-pressed={lang === l}
                >
                  {l === "en" ? "EN" : "PT"}
                </button>
              ))}
            </div>
          </div>
          <div>
            <h3 className="text-xs font-semibold uppercase tracking-wide text-muted">
              {t.landing.footerProduct}
            </h3>
            <ul className="mt-3 grid gap-2 text-sm">
              <li>
                <a href="#como-funciona" className="hover:text-teal">
                  {t.landing.howTitle}
                </a>
              </li>
              <li>
                <a href="#perguntas" className="hover:text-teal">
                  {t.landing.faqTitle}
                </a>
              </li>
            </ul>
          </div>
          <div>
            <h3 className="text-xs font-semibold uppercase tracking-wide text-muted">
              {t.landing.footerLegal}
            </h3>
            <ul className="mt-3 grid gap-2 text-sm text-muted">
              <li>{t.landing.footerTerms}</li>
              <li>{t.landing.footerPrivacy}</li>
            </ul>
          </div>
          <div>
            <h3 className="text-xs font-semibold uppercase tracking-wide text-muted">
              {t.landing.footerContact}
            </h3>
            <ul className="mt-3 grid gap-2 text-sm">
              <li>
                <a href="mailto:news@kloxus-ai.trendfi.com.br" className="break-all hover:text-teal">
                  news@kloxus-ai.trendfi.com.br
                </a>
              </li>
            </ul>
          </div>
        </div>
        <div className="border-t border-line">
          <div className="mx-auto max-w-6xl px-4 py-5 text-xs text-muted">© 2026 {t.appName}</div>
        </div>
      </footer>
    </div>
  );
}
