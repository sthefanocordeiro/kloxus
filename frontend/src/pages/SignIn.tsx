import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { SiteFooter, SiteHeader } from "../components/SiteChrome";
import { Button, Card } from "../ui";

// Account recovery (KLOX-272): the explicit "I already have an account"
// path. Submitting only mails a recovery link — it never creates the
// empty replacement workspace that used to strand returning users.
export function SignIn() {
  const { t, lang } = useT();
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState<{ email: string; login_url?: string } | null>(null);
  const login = useMutation({
    mutationFn: () => api.login(email.trim(), lang),
    onSuccess: (r) => setSent({ email: r.email, login_url: r.login_url }),
  });

  return (
    <div className="flex min-h-dvh flex-col bg-paper text-ink">
      <SiteHeader />
      <main className="mx-auto flex w-full max-w-xl flex-1 items-center px-4 py-12">
        <Card className="w-full p-6 md:p-8">
          <h1 className="font-doc text-3xl font-semibold leading-tight">{t.signin.title}</h1>
          <p className="mt-2 text-sm text-muted">{t.signin.lede}</p>
          {sent ? (
            <div className="mt-6">
              <p className="text-sm text-ink">{t.signin.sent(sent.email)}</p>
              {sent.login_url && (
                <a href={sent.login_url} className="mt-3 inline-block font-semibold text-teal underline underline-offset-2">
                  {t.signin.openLink}
                </a>
              )}
            </div>
          ) : (
            <form
              className="mt-6 grid gap-3"
              onSubmit={(e) => {
                e.preventDefault();
                if (email.trim()) login.mutate();
              }}
            >
              <label className="grid gap-1 text-sm font-medium">
                {t.signin.emailLabel}
                <input
                  type="email"
                  required
                  autoFocus
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder={t.claim.emailPlaceholder}
                  className="rounded-[var(--radius-input)] border border-line bg-paper px-3 py-2.5 text-sm text-ink"
                />
              </label>
              <div className="flex flex-wrap items-center gap-3">
                <Button disabled={login.isPending}>{login.isPending ? t.signin.sending : t.signin.action}</Button>
                <Link to="/start" className="text-sm text-teal underline underline-offset-2">
                  {t.signin.createInstead}
                </Link>
              </div>
              {login.error && <p className="text-sm text-danger">{errText(t, login.error)}</p>}
            </form>
          )}
        </Card>
      </main>
      <SiteFooter />
    </div>
  );
}
