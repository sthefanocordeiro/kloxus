import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { Menu, X } from "lucide-react";
import { api } from "../api";
import { useT, type Lang } from "../i18n";
import { cn } from "../lib/cn";
import { Button } from "../ui";

// Shared chrome for the public site surfaces — landing and the legal
// pages (KLOX-69/70). The sticky header carries the section links, the
// PT/EN switch (header on every width, per the owner review) and the
// primary CTA; on narrow widths the links, entry and CTA fold into a
// hamburger menu. Section anchors live on the landing, so from other
// pages they go through "/#id".

export function useSiteEntry() {
  const nav = useNavigate();
  const pubs = useQuery({ queryKey: ["publications"], queryFn: api.publications, retry: false });
  const start = useMutation({ mutationFn: api.workspace });
  const create = () => start.mutate(undefined, { onSuccess: () => nav("/new") });
  const signIn = () => start.mutate(undefined, { onSuccess: () => nav("/publications") });
  return { returning: (pubs.data?.length ?? 0) > 0, start, create, signIn };
}

// The hero/closing CTA and the header button share one definition —
// resolves-or-creates the visitor workspace, then lands on onboarding.
export function SiteCta({
  size = "md",
  className,
  onClick,
  entry,
}: {
  size?: "sm" | "md" | "lg";
  className?: string;
  onClick?: () => void;
  entry?: ReturnType<typeof useSiteEntry>;
}) {
  const { t } = useT();
  const own = useSiteEntry();
  const { start, create } = entry ?? own;
  return (
    <Button
      size={size}
      variant="accent"
      disabled={start.isPending}
      className={className}
      onClick={() => {
        onClick?.();
        create();
      }}
    >
      {start.isPending ? t.landing.starting : t.landing.cta}
    </Button>
  );
}

function LangToggle() {
  const { t, lang, setLang } = useT();
  return (
    <div
      className="flex items-center rounded-full border border-line p-0.5"
      role="group"
      aria-label={t.nav.language}
    >
      {(["pt", "en"] as Lang[]).map((l) => (
        <button
          key={l}
          onClick={() => setLang(l)}
          aria-pressed={lang === l}
          className={cn(
            "flex h-11 min-w-11 items-center justify-center rounded-full px-1 text-xs font-medium md:h-10 md:min-w-10",
            lang === l ? "bg-ink text-paper" : "text-muted hover:text-ink",
          )}
        >
          {l.toUpperCase()}
        </button>
      ))}
    </div>
  );
}

export function SiteHeader() {
  const { t } = useT();
  const { returning, start, signIn } = useSiteEntry();
  const [open, setOpen] = useState(false);
  const { pathname } = useLocation();
  const hash = (id: string) => (pathname === "/" ? `#${id}` : `/#${id}`);

  const entryCls =
    "inline-flex h-11 items-center rounded-[var(--radius-btn)] px-3 text-sm font-medium text-ink hover:bg-surface disabled:opacity-50 md:h-10";
  const entry = returning ? (
    <Link to="/publications" className={entryCls}>
      {t.landing.navDashboard}
    </Link>
  ) : (
    <button onClick={signIn} disabled={start.isPending} className={entryCls}>
      {t.landing.navSignIn}
    </button>
  );

  return (
    <header className="sticky top-0 z-40 border-b border-line bg-paper/90 backdrop-blur">
      <div className="mx-auto flex h-16 max-w-6xl items-center gap-4 px-4">
        <Link
          to="/"
          onClick={() => setOpen(false)}
          className="font-display text-lg font-extrabold tracking-tight text-teal"
        >
          {t.appName}
        </Link>
        <nav className="hidden items-center gap-5 text-sm text-muted md:flex">
          <a href={hash("como-funciona")} className="hover:text-ink">
            {t.landing.howTitle}
          </a>
          <a href={hash("perguntas")} className="hover:text-ink">
            {t.landing.faqTitle}
          </a>
        </nav>
        <div className="ml-auto flex items-center gap-2">
          <LangToggle />
          <span className="hidden md:inline-flex">{entry}</span>
          <SiteCta size="sm" className="hidden md:inline-flex" />
          <button
            className="flex h-11 w-11 items-center justify-center rounded-[var(--radius-btn)] text-ink hover:bg-surface md:hidden"
            aria-expanded={open}
            aria-controls="site-menu"
            aria-label={t.landing.menu}
            onClick={() => setOpen((o) => !o)}
          >
            {open ? <X size={20} aria-hidden /> : <Menu size={20} aria-hidden />}
          </button>
        </div>
      </div>
      {open && (
        <div id="site-menu" className="border-t border-line bg-paper px-4 pb-5 pt-1 md:hidden">
          <nav className="grid text-[15px] font-medium text-ink">
            <a
              href={hash("como-funciona")}
              onClick={() => setOpen(false)}
              className="flex h-11 items-center border-b border-line/60"
            >
              {t.landing.howTitle}
            </a>
            <a
              href={hash("perguntas")}
              onClick={() => setOpen(false)}
              className="flex h-11 items-center border-b border-line/60"
            >
              {t.landing.faqTitle}
            </a>
            {returning ? (
              <Link
                to="/publications"
                onClick={() => setOpen(false)}
                className="flex h-11 items-center"
              >
                {t.landing.navDashboard}
              </Link>
            ) : (
              <button
                onClick={() => {
                  setOpen(false);
                  signIn();
                }}
                disabled={start.isPending}
                className="flex h-11 items-center text-left disabled:opacity-50"
              >
                {t.landing.navSignIn}
              </button>
            )}
          </nav>
          <SiteCta size="md" className="mt-3 w-full" onClick={() => setOpen(false)} />
        </div>
      )}
    </header>
  );
}

export function SiteFooter() {
  const { t } = useT();
  const { pathname } = useLocation();
  const hash = (id: string) => (pathname === "/" ? `#${id}` : `/#${id}`);
  return (
    <footer className="border-t border-line bg-paper">
      <div className="mx-auto grid max-w-6xl gap-8 px-4 py-12 sm:grid-cols-2 md:grid-cols-[1.5fr_1fr_1fr_1.5fr]">
        <div>
          <p className="font-display text-lg font-extrabold tracking-tight text-teal">
            {t.appName}
          </p>
        </div>
        <div>
          <h3 className="text-xs font-semibold uppercase tracking-wide text-muted">
            {t.landing.footerProduct}
          </h3>
          <ul className="mt-3 grid gap-2 text-sm">
            <li>
              <a href={hash("como-funciona")} className="hover:text-teal">
                {t.landing.howTitle}
              </a>
            </li>
            <li>
              <a href={hash("perguntas")} className="hover:text-teal">
                {t.landing.faqTitle}
              </a>
            </li>
          </ul>
        </div>
        <div>
          <h3 className="text-xs font-semibold uppercase tracking-wide text-muted">
            {t.landing.footerLegal}
          </h3>
          <ul className="mt-3 grid gap-2 text-sm">
            <li>
              <Link to="/termos" className="hover:text-teal">
                {t.landing.footerTerms}
              </Link>
            </li>
            <li>
              <Link to="/privacidade" className="hover:text-teal">
                {t.landing.footerPrivacy}
              </Link>
            </li>
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
  );
}
