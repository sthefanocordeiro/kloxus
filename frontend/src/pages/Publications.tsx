import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Button, Card } from "../ui";

export function Publications() {
  const { t } = useT();
  const qc = useQueryClient();
  const pubs = useQuery({ queryKey: ["publications"], queryFn: api.publications });
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });

  // demo reset + new workspace mutate server-side state wholesale —
  // drop every cached query so nothing stale survives
  const reset = useMutation({
    mutationFn: api.demoReset,
    onSuccess: () => qc.clear(),
  });
  const newWs = useMutation({
    mutationFn: api.demoNew,
    onSuccess: () => qc.clear(),
  });
  const demoErr = reset.error ?? newWs.error;

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-8">
      <p className="mb-1 text-xs uppercase tracking-wide text-muted">
        {ws.data ? `${t.workspace}: ${ws.data.label}` : ""}
      </p>
      <h1 className="mb-6 font-doc text-2xl font-semibold">{t.publications}</h1>
      {pubs.isLoading && <p className="text-muted">…</p>}
      {pubs.data?.length === 0 && (
        <Card className="p-6 text-center">
          <p className="text-sm text-muted">{t.landing.lede}</p>
          <Link to="/new" className="mt-3 inline-block">
            <Button>{t.landing.start}</Button>
          </Link>
        </Card>
      )}
      <div className="grid gap-3">
        {pubs.data?.map((p) => (
          <Link key={p.id} to={`/pub/${p.id}`}>
            <Card className="hover:border-ink/40 transition-colors">
              <div className="flex items-baseline justify-between">
                <h2 className="font-doc text-lg font-semibold">{p.name}</h2>
                <span className="text-xs text-muted">
                  {(t.frequency as Record<string, string>)[p.frequency] ?? p.frequency}
                </span>
              </div>
              <p className="mt-1 line-clamp-2 text-sm text-muted">{p.editorial_line}</p>
            </Card>
          </Link>
        ))}
      </div>

      {ws.data?.mode === "demo" && (
        <Card className="mt-8 text-sm">
          <h2 className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">
            {t.demo.title}
          </h2>
          <div className="flex flex-wrap items-start gap-3">
            <div className="flex-1 min-w-48">
              <Button
                variant="outline"
                size="sm"
                disabled={reset.isPending}
                onClick={() => reset.mutate()}
              >
                {reset.isPending ? "…" : t.demo.reset}
              </Button>
              <p className="mt-1 text-xs text-muted">
                {reset.isSuccess ? t.demo.resetDone : t.demo.resetHint}
              </p>
            </div>
            <div className="flex-1 min-w-48">
              <Button
                variant="outline"
                size="sm"
                disabled={newWs.isPending}
                onClick={() => newWs.mutate()}
              >
                {newWs.isPending ? "…" : t.demo.newWs}
              </Button>
              <p className="mt-1 text-xs text-muted">{t.demo.newHint}</p>
            </div>
          </div>
          {demoErr && <p className="mt-2 text-xs text-danger">{errText(t, demoErr)}</p>}
        </Card>
      )}
    </div>
  );
}
