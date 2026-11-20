import { useMutation, useQuery } from "@tanstack/react-query";
import { Outlet, useParams, useSearchParams } from "react-router-dom";
import { api } from "../api";
import { useT, type Lang } from "../i18n";
import { useReader } from "./readerStrings";
import { Markdown } from "../markdown";
import { fmtDate } from "../lib/fmt";
import { Badge, Button, Card } from "../ui";
import { errText } from "../lib/errors";
import { useEffect, useState } from "react";

// ReaderShell is the chrome for reader-facing pages: the publication
// name and a language switch — never editor navigation.
export function ReaderShell() {
  const { t, lang, setLang } = useT();
  return (
    <div className="min-h-dvh bg-paper">
      <header className="border-b border-line bg-paper-raised">
        <div className="mx-auto flex h-12 max-w-2xl items-center gap-4 px-4">
          <span className="font-doc text-lg font-semibold tracking-tight">{t.appName}</span>
          <span className="text-xs text-muted">{t.reader.subscriberView}</span>
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
      <main>
        <Outlet />
      </main>
    </div>
  );
}

// The public issue page (/p/<slug>/<number>): the issue as a reader sees
// it — paid sections gated for non-subscribers, a magic link unlocks.
export function PublicIssue() {
  const { slug = "", num = "" } = useParams();
  const [params] = useSearchParams();
  const { t, lang } = useT();
  const query = params.toString() ? `?${params.toString()}` : "";
  const q = useQuery({
    queryKey: ["publicIssue", slug, num, query],
    queryFn: () => api.publicIssue(slug, num, query),
    retry: false,
  });
  const tokQuery = params.get("tok") ? `?tok=${params.get("tok")}` : "";
  const d = q.data;

  // the browser tab is the edition's title (KLOX-66)
  useEffect(() => {
    if (d) {
      const prev = document.title;
      document.title = `${d.issue.subject} · ${d.publication.name}`;
      return () => { document.title = prev; };
    }
  }, [d]);

  if (q.isLoading) return <div className="mx-auto max-w-2xl px-4 py-16 text-sm text-muted">…</div>;
  if (q.isError || !d)
    return (
      <div className="mx-auto max-w-2xl px-4 py-16 text-center">
        <h1 className="font-doc text-xl font-semibold">{d?.publication.name ?? t.appName}</h1>
        <p className="mt-2 text-sm text-muted">{t.errors.not_found}</p>
      </div>
    );

  const toggleFree = () => {
    const next = new URLSearchParams(params);
    if (params.get("view") === "free") next.delete("view");
    else next.set("view", "free");
    window.location.search = next.toString();
  };

  return (
    <div className="mx-auto max-w-2xl px-4 py-10">
      <p className="text-xs font-semibold uppercase tracking-wide text-muted">
        {d.publication.name} · #{d.issue.number} · {fmtDate(d.issue.created_at, lang)}
      </p>
      <h1 className="mt-1 mb-6 font-doc text-3xl font-semibold leading-tight">{d.issue.subject}</h1>

      <article className="font-doc text-[15px] leading-relaxed">
        <Markdown md={d.body_md.replace(/^#\s.*(\n+|$)/, "")} />
      </article>

      {d.gated && (
        <Card className="mt-8 border-ink/20 bg-paper-raised p-5">
          <Badge tone="ink">{t.reader.forPaying}</Badge>
          <p className="mt-2 text-sm text-muted">{t.reader.unlockHint}</p>
          {/* price + what the subscription unlocks (KLOX-66) */}
          {d.paywall?.enabled && d.paywall.price && (
            <p className="mt-3 font-doc text-lg font-semibold">{d.paywall.price}</p>
          )}
          {(d.paid_sections ?? []).length > 0 && (
            <p className="mt-1 text-sm text-muted">
              {t.reader.unlocksSections} {d.paid_sections.join(", ")}
            </p>
          )}
          {d.paywall?.enabled && (
            <PayGate slug={d.publication.slug} num={d.issue.number} tokQuery={tokQuery} paywall={d.paywall} />
          )}
        </Card>
      )}
      {d.subscribed && (
        <div className="mt-6 flex items-center gap-3 text-xs">
          <Badge tone="teal">{t.reader.subscribed}</Badge>
          <button className="text-muted underline hover:text-ink" onClick={toggleFree}>
            {params.get("view") === "free" ? t.reader.viewFull : t.reader.viewFree}
          </button>
        </div>
      )}

      <SubscribeCard slug={d.publication.slug} tokQuery={tokQuery} />
    </div>
  );
}

// Paid checkout (KLOX-13): the reader's email opens a one-time PIX
// billing at AbacatePay — or the simulated fallback the API flags — and
// the hosted page returns through ?pay=<ref>, which polls the status
// endpoint until the grant lands. In outbox mode the API re-serves the
// access link so the demo completes without an inbox.
function PayGate({
  slug,
  num,
  tokQuery,
  paywall,
}: {
  slug: string;
  num: number;
  tokQuery: string;
  paywall?: { enabled: boolean; price: string; test_mode?: boolean; simulated?: boolean };
}) {
  const { t } = useT();
  const r = useReader();
  const [params] = useSearchParams();
  const [email, setEmail] = useState("");
  const payRef = params.get("pay") ?? "";
  const co = useMutation({
    mutationFn: () => api.payCheckout(slug, email.trim(), String(num), tokQuery),
    onSuccess: (r) => {
      if (r.checkout_url) window.location.assign(r.checkout_url);
    },
  });
  const st = useQuery({
    queryKey: ["payStatus", slug, payRef],
    queryFn: () => api.payStatus(slug, payRef, tokQuery),
    enabled: payRef !== "",
    retry: false,
    refetchInterval: (q) => {
      const s = q.state.data?.status;
      return s === "paid" || s === "expired" || s === "cancelled" ? false : 3000;
    },
  });
  const done = co.data;

  if (payRef && st.data && st.data.status !== "pending") {
    const s = st.data;
    if (s.status === "paid") {
      return (
        <div className="mt-3 text-sm">
          <p className="text-muted">{r.payDone}</p>
          {s.magic_link && (
            <a href={s.magic_link} className="mt-1 inline-block font-semibold text-teal underline">
              {r.payOpen} →
            </a>
          )}
        </div>
      );
    }
    return <p className="mt-3 text-sm text-muted">{r.payExpired}</p>;
  }
  if (payRef) {
    return <p className="mt-3 text-sm text-muted">{r.payPending}</p>;
  }
  if (done?.already_subscribed) {
    return <p className="mt-3 text-sm text-muted">{r.alreadyPaid}</p>;
  }
  return (
    <form
      className="mt-3 flex flex-wrap items-center gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (email.trim()) co.mutate();
      }}
    >
      <input
        type="email"
        required
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        placeholder={r.emailPlaceholder}
        aria-label={r.payEmail}
        className="w-64 rounded border border-line bg-paper px-2 py-1.5 text-sm text-ink"
      />
      <Button size="sm" disabled={co.isPending}>
        {co.isPending ? r.payStarting : r.payAction}
      </Button>
      {paywall?.test_mode && !paywall.simulated && (
        <span className="text-xs text-muted">{r.testMode}</span>
      )}
      {paywall?.simulated && <span className="text-xs text-muted">{r.simMode}</span>}
      {co.error && <p className="w-full text-xs text-danger">{errText(t, co.error)}</p>}
    </form>
  );
}

// Double opt-in signup: the address gets one confirmation email and
// receives nothing until the link inside it is opened. In outbox mode
// the API answers with the link itself so the demo can complete it.
function SubscribeCard({ slug, tokQuery }: { slug: string; tokQuery: string }) {
  const { t } = useT();
  const [email, setEmail] = useState("");
  const [done, setDone] = useState<{ confirm_url?: string; already?: boolean } | null>(null);
  const sub = useMutation({
    mutationFn: () => api.subscribe(slug, email.trim(), tokQuery),
    onSuccess: (r) => setDone({ confirm_url: r.confirm_url, already: r.already_confirmed }),
  });
  return (
    <Card className="mt-8 border-line bg-paper-raised p-5">
      <p className="text-sm font-semibold">{t.reader.subscribeEmail}</p>
      {done ? (
        <p className="mt-2 text-sm text-muted">
          {done.already ? t.reader.alreadySubscribed : t.reader.confirmSent}
          {done.confirm_url && (
            <a href={done.confirm_url} className="ml-2 font-semibold text-teal underline">
              {t.reader.confirmLink}
            </a>
          )}
        </p>
      ) : (
        <form
          className="mt-3 flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (email.trim()) sub.mutate();
          }}
        >
          <input
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder={t.reader.emailPlaceholder}
            className="w-64 rounded border border-line bg-paper px-2 py-1.5 text-sm text-ink"
          />
          <Button size="sm" disabled={sub.isPending}>
            {sub.isPending ? t.reader.subscribing : t.reader.subscribeEmailAction}
          </Button>
        </form>
      )}
      {sub.error && <p className="mt-2 text-xs text-danger">{errText(t, sub.error)}</p>}
    </Card>
  );
}

// The one-click unsubscribe target carried by every issue email —
// performs the opt-out once and confirms in the publication's voice.
export function UnsubPage() {
  const [params] = useSearchParams();
  const { t } = useT();
  const q = useQuery({
    queryKey: ["unsub", params.toString()],
    queryFn: () =>
      fetch(`/api/v1/unsub?${params.toString()}`, { headers: { Accept: "application/json" } }).then(
        async (r) => {
          if (!r.ok) throw new Error(String(r.status));
          return r.json() as Promise<{ unsubscribed: boolean }>;
        },
      ),
    retry: false,
  });
  return (
    <div className="mx-auto max-w-xl px-4 py-16 text-center">
      {q.isLoading && <p className="text-sm text-muted">…</p>}
      {q.data?.unsubscribed && (
        <>
          <h1 className="font-doc text-2xl font-semibold">{t.reader.unsubscribed}</h1>
          <p className="mt-2 text-sm text-muted">{t.reader.unsubHint}</p>
        </>
      )}
      {q.isError && (
        <>
          <h1 className="font-doc text-2xl font-semibold">{t.errors.not_found}</h1>
          <p className="mt-2 text-sm text-muted">{t.reader.unsubFailed}</p>
        </>
      )}
    </div>
  );
}
