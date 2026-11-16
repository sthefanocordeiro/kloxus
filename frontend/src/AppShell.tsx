import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, Outlet, useLocation } from "react-router-dom";
import { api } from "./api";
import { useT, type Lang } from "./i18n";
import { Button } from "./ui";
import { errText } from "./lib/errors";

// The chrome around every screen: brand, language switch, and the
// workspace-claim banner when live mail still has no verified owner.
export function AppShell() {
  const { t, lang, setLang } = useT();
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace, retry: false, staleTime: Infinity });
  const demo = ws.data?.mode === "demo";
  return (
    <div className="h-dvh flex flex-col">
      <header className="border-b border-line bg-paper-raised">
        <div className="mx-auto flex h-12 max-w-[1400px] items-center gap-4 px-4">
          <Link to="/" className="font-doc text-lg font-semibold tracking-tight">
            {t.appName}
          </Link>
          {demo && <span className="text-xs text-muted">demo</span>}
          <div className="ml-auto flex items-center gap-1 text-xs">
            {(["en", "pt"] as Lang[]).map((l) => (
              <button
                key={l}
                onClick={() => setLang(l)}
                className={
                  "rounded px-2 py-1 " +
                  (lang === l ? "bg-ink text-paper font-medium" : "text-muted hover:text-ink")
                }
                aria-pressed={lang === l}
              >
                {l === "en" ? "EN" : "PT"}
              </button>
            ))}
          </div>
        </div>
      </header>
      <ClaimBanner />
      <main className="flex-1 min-h-0 flex flex-col overflow-y-auto">
        <Outlet />
      </main>
    </div>
  );
}

// Live mail needs a verified owner: until the workspace is claimed this
// banner mails the creator a single-use claim link. Outbox mode,
// claimed workspaces and the landing page see nothing here. The app
// palette is light-only, so no dark: overrides — under a dark system
// theme the banner keeps its readable light colors, always on one line.
function ClaimBanner() {
  const { t } = useT();
  const { pathname } = useLocation();
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState<{ email: string; claim_url?: string } | null>(null);
  const claim = useMutation({
    mutationFn: () => api.claim(email.trim()),
    onSuccess: (r) => setSent({ email: r.email, claim_url: r.claim_url }),
  });
  const w = ws.data;
  if (pathname === "/" || !w || w.mail !== "live" || w.claimed) return null;
  return (
    <div className="border-b border-amber-300 bg-amber-50 text-ink">
      <div className="mx-auto flex max-w-[1400px] flex-nowrap items-center gap-3 overflow-x-auto whitespace-nowrap px-4 py-2 text-sm">
        <span className="truncate">{t.claim.banner}</span>
        {sent ? (
          <span className="text-muted">
            {t.claim.sent(sent.email)}
            {sent.claim_url && (
              <a href={sent.claim_url} className="ml-2 font-semibold text-teal underline">
                {t.claim.openLink}
              </a>
            )}
          </span>
        ) : (
          <form
            className="flex shrink-0 items-center gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (email.trim()) claim.mutate();
            }}
          >
            <input
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder={t.claim.emailPlaceholder}
              className="rounded border border-line bg-paper px-2 py-1 text-xs text-ink"
            />
            <Button size="sm" variant="teal" disabled={claim.isPending}>
              {claim.isPending ? t.claim.sending : t.claim.action}
            </Button>
          </form>
        )}
        {claim.error && <span className="text-xs text-danger">{errText(t, claim.error)}</span>}
      </div>
    </div>
  );
}
