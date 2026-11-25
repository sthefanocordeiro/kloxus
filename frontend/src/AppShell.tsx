import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, NavLink, Outlet, matchPath, useLocation } from "react-router-dom";
import {
  CircleUser,
  LogOut,
  Ellipsis,
  Layers,
  Newspaper,
  Plus,
  Rss,
} from "lucide-react";
import { api, pautaPath } from "./api";
import { useT, type Lang, type T } from "./i18n";
import { cn } from "./lib/cn";
import { BackButton } from "./components/BackButton";
import { ClaimEmail } from "./components/ClaimEmail";
import { usePublishing } from "./lib/publishing";

interface Crumb {
  label: string;
  to: string;
}
interface Chrome {
  title: string;
  crumbs: Crumb[];
  section: string;
}

// /issues/:id redirects to /pauta/:id (KLOX-179) — the pauta editor is
// the only edition surface, so both URLs count as "edition" chrome.
const matchIssue = (pathname: string) =>
  matchPath("/issues/:id", pathname) ??
  matchPath("/pauta/:id", pathname) ??
  matchPath("/pauta/:id/export", pathname);

function chromeFor(pathname: string, t: T, pubName?: string, issueNumber?: number, issuePubID?: number): Chrome {
  const pubM = matchPath("/publications/:id/*", pathname) ?? matchPath("/publications/:id", pathname);
  const issueM = matchIssue(pathname);
  const pub = pubM?.params.id;
  const pubCrumb: Crumb = { label: pubName ?? t.publications, to: pub ? `/publications/${pub}` : "/publications" };

  // /new's crumb used to point at /new itself — a Voltar that went
  // nowhere (KLOX-79). Publicações is the real parent.
  if (pathname === "/start") return { title: t.nav.creating, crumbs: [], section: "new" };
  // the publications list is Radar-level chrome — highlight the "Radar"
  // nav item, not "Pautas" (KLOX-185)
  if (pathname === "/publications") return { title: t.publications, crumbs: [], section: "radar" };
  if (issueM) {
    // KLOX-187: the pauta pages used to show a bare "Kloxus" header —
    // the trail is Radar › Pauta, and on export Pauta › Exportar so
    // "← Voltar" lands back on the editor, like every other screen.
    const radarCrumb: Crumb = {
      label: t.publications,
      to: issuePubID ? `/publications/${issuePubID}` : "/publications",
    };
    const pautaLabel = issueNumber ? `${t.issue} ${issueNumber}` : t.issue;
    if (pathname.endsWith("/export")) {
      return {
        title: t.nav.export,
        crumbs: [radarCrumb, { label: pautaLabel, to: `/pauta/${issueM.params.id}` }],
        section: "edition",
      };
    }
    return { title: pautaLabel, crumbs: [radarCrumb], section: "edition" };
  }
  if (pub) {
    const tail = pathname.split("/").pop() ?? "";
    const map: Record<string, [string, string]> = {
      today: [t.nav.today, "today"],
      sources: [t.nav.radar, "radar"],
    };
    const [title, section] = map[tail] ?? [pubName ?? t.nav.editions, "editions"];
    const crumbs = tail === pub ? [] : [pubCrumb];
    return { title, crumbs, section };
  }
  return { title: t.appName, crumbs: [], section: "" };
}

export function AppShell() {
  const { t, lang, setLang } = useT();
  const { pathname } = useLocation();
  const publishing = usePublishing();
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace, retry: false, staleTime: Infinity });

  const pubM = matchPath("/publications/:id/*", pathname) ?? matchPath("/publications/:id", pathname);
  const issueM = matchIssue(pathname);
  const pubId = pubM?.params.id;
  const issueId = issueM?.params.id ? Number(issueM.params.id) : undefined;

  const issue = useQuery({
    queryKey: ["issue", issueId],
    queryFn: () => api.issue(issueId!),
    enabled: !!issueId,
  });
  const pubs = useQuery({ queryKey: ["publications"], queryFn: api.publications });
  // off a publication route (e.g. /publications itself) fall back to the
  // account's Radar so pub-scoped links still resolve (KLOX-185)
  const pubKey =
    pubId ??
    (issue.data?.publication_id ? String(issue.data.publication_id) : undefined) ??
    (pubs.data?.length ? String(pubs.data[0].id) : undefined);
  const pubQ = useQuery({
    queryKey: ["publication", pubKey],
    queryFn: () => api.publication(pubKey!),
    enabled: !!pubKey,
  });

  const pub = pubQ.data?.publication;
  const latest = pubQ.data?.latest_issue;
  const chrome = chromeFor(pathname, t, pub?.name, issue.data?.number, issue.data?.publication_id);
  // Nav targets come from the URL pubId (or the issue's publication_id on
  // /pauta/:id), not the fetched entity — deriving them from `pub` left
  // every pub-scoped link pointing at /publications/<word> until pubQ
  // resolved, a dead route that 404s and never recovers (KLOX-74).
  const pubBase = pubKey ? `/publications/${pubKey}` : "/publications";
  const editionTo = latest ? pautaPath(latest.id, latest.exported ? "exported" : latest.status) : pubBase;

  // KLOXUS_PUBLISHING off (KLOX-122): Hoje · Pautas · Radar — one
  // Radar screen holds the old Fontes + Configurações (KLOX-207).
  // Readers, send and the outbox are not reachable from the nav.
  const items = publishing
    ? [
        { to: editionTo, label: t.nav.edition, icon: Newspaper, section: "edition", end: true },
        { to: pubBase, label: t.nav.editions, icon: Layers, section: "editions", end: true },
        { to: `${pubBase}/sources`, label: t.nav.radar, icon: Rss, section: "radar" },
      ]
    : [
        { to: `${pubBase}/today`, label: t.nav.today, icon: Newspaper, section: "today", end: true },
        { to: pubBase, label: t.nav.editions, icon: Layers, section: "editions", end: true },
        { to: `${pubBase}/sources`, label: t.nav.radar, icon: Rss, section: "radar" },
      ];
  const section = chrome.section;

  // KLOX-187: before the first Radar exists the nav's destinations all
  // 404 — hide the items (sidebar and bottom tabs) until there is one.
  // pubs.data === undefined means still loading: keep the items rather
  // than flash an empty shell on every page.
  const noRadar = pubs.data !== undefined && pubs.data.length === 0;
  const navItems = noRadar ? [] : items;

  // top-level screens (the nav's own destinations) have no "← Voltar"
  const tail = pathname.split("/").pop() ?? "";
  const topLevel = !!pubM && ["today", "sources", "readers", "sections"].includes(tail);
  const back = topLevel ? undefined : chrome.crumbs[chrome.crumbs.length - 1];

  return (
    <div className="flex h-dvh">
      {/* desktop sidebar — 240 px (§3.1) */}
      <aside className="hidden w-60 shrink-0 flex-col border-r border-line bg-surface md:flex">
        <div className="flex h-14 items-center px-4">
          <Link to="/" className="font-display text-lg font-extrabold tracking-tight text-teal">
            {t.appName}
          </Link>
        </div>
        <div className="px-3 pb-3">
          {/* one newsletter per account (KLOX-66) — no switcher; the
              sidebar shows the newsletter's name, or the create link
              when the account still has none */}
          {pub ? (
            <Link
              to={`${pubBase}/today`}
              className="flex h-10 w-full items-center truncate rounded-[var(--radius-input)] border border-line bg-paper-raised px-3 text-sm font-medium text-ink hover:border-teal/50"
            >
              <span className="truncate">{pub.name}</span>
            </Link>
          ) : pubs.data?.length ? (
            <Link
              to={`/publications/${pubs.data[0].id}/today`}
              className="flex h-10 w-full items-center truncate rounded-[var(--radius-input)] border border-line bg-paper-raised px-3 text-sm font-medium text-ink hover:border-teal/50"
            >
              <span className="truncate">{pubs.data[0].name}</span>
            </Link>
          ) : null}
        </div>
        <nav className="flex-1 space-y-0.5 overflow-y-auto px-3" aria-label={t.publications}>
          {navItems.map((it) => (
            <NavLink
              key={it.section}
              to={it.to}
              end={it.end}
              // Highlight follows the route's section, never the link's
              // path match: with no edition yet the edition item points
              // at pubBase, where a prefix match would glow on every
              // publication sub-page (KLOX-90).
              className={() =>
                cn(
                  "flex h-10 items-center gap-2.5 rounded-[var(--radius-btn)] px-3 text-sm font-medium transition-colors duration-150",
                  section === it.section
                    ? "bg-teal-soft text-teal"
                    : "text-muted hover:bg-line/50 hover:text-ink",
                )
              }
            >
              <it.icon size={16} aria-hidden />
              {it.label}
            </NavLink>
          ))}
        </nav>
        {pubs.data?.length === 0 && (
          <div className="border-t border-line p-3">
            <Link to="/start" className="flex h-10 items-center gap-2.5 rounded-[var(--radius-btn)] px-3 text-sm font-medium text-muted hover:bg-line/50 hover:text-ink">
              <Plus size={16} aria-hidden />
              {t.nav.newNewsletter}
            </Link>
          </div>
        )}
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        {/* top bar — breadcrumb + page title left, one primary action right */}
        <header className="flex h-14 shrink-0 items-center gap-3 border-b border-line bg-paper-raised px-4">
          {back && <BackButton to={back.to} dest={back.label} />}
          <div className="min-w-0">
            {chrome.crumbs.length > 0 && (
              <nav aria-label="breadcrumb" className="hidden sm:block">
                <ol className="flex items-center gap-1 text-xs text-muted">
                  {chrome.crumbs.map((c) => (
                    <li key={c.to}>
                      <Link to={c.to} className="hover:text-ink hover:underline">
                        {c.label}
                      </Link>
                    </li>
                  ))}
                </ol>
              </nav>
            )}
            <h1 className="truncate font-display text-base font-bold leading-tight">{chrome.title}</h1>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <div id="shell-primary-slot" className="flex items-center gap-2" />
            <LangSwitch lang={lang} setLang={setLang} className="hidden md:flex" />
            <Drop
              trigger={(open) => (
                <button
                  className="inline-flex h-11 w-11 items-center justify-center rounded-full text-muted hover:bg-surface hover:text-ink md:h-10 md:w-10"
                  aria-label={t.nav.account}
                  title={t.nav.account}
                  aria-expanded={open}
                  aria-haspopup="menu"
                >
                  <CircleUser size={22} aria-hidden />
                </button>
              )}
            >
              <AccountMenu claimed={ws.data?.claimed} email={ws.data?.owner_email} />
            </Drop>
          </div>
        </header>

        <main className="min-h-0 flex-1 overflow-y-auto pb-20 md:pb-0">
          <Outlet />
        </main>

        {/* mobile bottom tabs — first nav items + "Mais" overflow (§3.1) */}
        {navItems.length > 0 && (
        <nav
          className={cn(
            "fixed inset-x-0 bottom-0 z-40 grid border-t border-line bg-paper-raised md:hidden",
            publishing ? "grid-cols-4" : "grid-cols-3",
          )}
          style={{ paddingBottom: "env(safe-area-inset-bottom)" }}
          aria-label={t.publications}
        >
          {(publishing ? navItems.slice(0, 3) : navItems)
            .map((tab) => (
            <Link
              key={tab.label}
              to={tab.to}
              className={cn(
                "flex h-14 flex-col items-center justify-center gap-0.5 text-[11px] font-medium",
                section === tab.section ? "text-teal" : "text-muted",
              )}
            >
              <tab.icon size={20} aria-hidden />
              {tab.label}
            </Link>
          ))}
          {publishing && <Drop
            side="top"
            className="h-full"
            trigger={(open) => (
              <button
                className="flex h-14 w-full flex-col items-center justify-center gap-0.5 text-[11px] font-medium text-muted"
                aria-label={t.nav.more}
                aria-expanded={open}
                aria-haspopup="menu"
              >
                <Ellipsis size={20} aria-hidden />
                {t.nav.more}
              </button>
            )}
          >
            <MoreMenu pubBase={pubBase} />
          </Drop>}
        </nav>
        )}
      </div>
    </div>
  );
}

// A featherweight dropdown for the shell's two menus — the Radix
// popover stays in the lazy route chunks; pulling it into this eager
// module would blow the landing bundle budget (KLOX-16).
function Drop({
  trigger,
  children,
  side = "bottom",
  className,
}: {
  trigger: (open: boolean) => ReactNode;
  children: ReactNode;
  side?: "top" | "bottom";
  // lets a grid cell stretch its trigger — the bottom tab bar's "Mais"
  // button shrank to its text (25 px wide) without it (KLOX-68)
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);
  return (
    <div ref={ref} className={cn("relative", className)}>
      <span onClick={() => setOpen((o) => !o)} className="flex h-full w-full">{trigger(open)}</span>
      {open && (
        <div
          className={cn(
            "absolute z-50 w-72 rounded-[var(--radius-card)] border border-line bg-paper-raised p-4 shadow-lg",
            side === "bottom" ? "right-0 top-full mt-1.5" : "bottom-full right-0 mb-1.5",
          )}
        >
          {children}
        </div>
      )}
    </div>
  );
}

function LangSwitch({ lang, setLang, className }: { lang: Lang; setLang: (l: Lang) => void; className?: string }) {
  return (
    <div className={cn("flex items-center gap-1 text-xs", className)}>
      {(["pt", "en"] as Lang[]).map((l) => (
        <button
          key={l}
          onClick={() => setLang(l)}
          className={cn(
            "flex h-11 min-w-11 items-center justify-center rounded-md px-1.5 md:h-10 md:min-w-10",
            lang === l ? "bg-ink font-medium text-paper" : "text-muted hover:text-ink",
          )}
          aria-pressed={lang === l}
        >
          {l === "en" ? "EN" : "PT"}
        </button>
      ))}
    </div>
  );
}

function AccountMenu({ claimed, email }: { claimed?: boolean; email?: string }) {
  const { t } = useT();
  // one Radar per account: a new Radar means signing out and starting
  // fresh; the claimed account comes back with an e-mail link
  const leave = async (to: string) => {
    try {
      await api.logout();
    } catch {
      return;
    }
    window.location.assign(to);
  };
  const item = "flex h-11 w-full items-center gap-3 rounded-[var(--radius-btn)] px-2 text-sm font-medium text-ink hover:bg-surface";
  return (
    <div className="space-y-3">
      <p className="text-xs font-semibold uppercase tracking-wide text-muted">{t.nav.account}</p>
      {claimed && email ? (
        <>
          <p className="text-sm text-ink">{t.nav.savedWith(email)}</p>
          <div className="grid gap-1 border-t border-line pt-2">
            <button type="button" className={item} onClick={() => leave("/start")}>
              <Plus size={16} aria-hidden className="text-muted" />
              {t.nav.newNewsletter}
            </button>
            <button type="button" className={item} onClick={() => leave("/")}>
              <LogOut size={16} aria-hidden className="text-muted" />
              {t.nav.signOut}
            </button>
          </div>
        </>
      ) : (
        <div className="space-y-2">
          <p className="text-sm font-medium">{t.nav.saveWithEmail}</p>
          <ClaimEmail />
        </div>
      )}
    </div>
  );
}

function MoreMenu({ pubBase }: { pubBase: string }) {
  const { t, lang, setLang } = useT();
  const links = [
    { to: `${pubBase}/sources`, label: t.nav.radar, icon: Rss },
    { to: "/publications", label: t.publications, icon: Layers },
  ];
  return (
    <div>
      <nav className="grid gap-1" aria-label={t.nav.more}>
        {links.map((l) => (
          <Link key={l.to} to={l.to} className="flex h-11 items-center gap-3 rounded-[var(--radius-btn)] px-2 text-sm font-medium text-ink hover:bg-surface">
            <l.icon size={16} aria-hidden className="text-muted" />
            {l.label}
          </Link>
        ))}
      </nav>
      <div className="mt-3 flex items-center justify-between border-t border-line pt-3">
        <span className="text-xs text-muted">{t.nav.language}</span>
        <LangSwitch lang={lang} setLang={setLang} />
      </div>
    </div>
  );
}
