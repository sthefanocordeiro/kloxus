import { useMutation, useQuery } from "@tanstack/react-query";
import { Outlet, useParams, useSearchParams } from "react-router-dom";
import { api } from "../api";
import { useT, type Lang } from "../i18n";
import { Markdown } from "../markdown";
import { fmtDate } from "../lib/fmt";
import { Badge, Button, Card } from "../ui";
import { useState } from "react";

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
  const [simDone, setSimDone] = useState<string | null>(null);
  const wsQuery = params.get("ws") ? `?ws=${params.get("ws")}` : "";
  const sim = useMutation({
    // the slug works where an id would — the API resolves both; ws
    // resolves the creator's workspace for cross-browser readers
    mutationFn: () => api.paywallSimulate(q.data!.publication.slug, wsQuery),
    onSuccess: (r) => setSimDone(r.magic_link),
  });
  const d = q.data;

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
        <Markdown md={d.body_md} />
      </article>

      {d.gated && (
        <Card className="mt-8 border-ink/20 bg-paper-raised p-5">
          <Badge tone="ink">{t.reader.forPaying}</Badge>
          <p className="mt-2 text-sm text-muted">{t.reader.unlockHint}</p>
          {!simDone ? (
            <Button className="mt-3" variant="teal" disabled={sim.isPending} onClick={() => sim.mutate()}>
              {t.reader.subscribe}
            </Button>
          ) : (
            <div className="mt-3 text-sm">
              <p className="text-muted">{t.reader.checkOutbox}</p>
              <a href={simDone} className="mt-1 inline-block font-semibold text-teal underline">
                {d.publication.name} →
              </a>
            </div>
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
    </div>
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
