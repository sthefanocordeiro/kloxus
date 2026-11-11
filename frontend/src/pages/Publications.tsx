import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../api";
import { useT } from "../i18n";
import { Card } from "../ui";

export function Publications() {
  const { t } = useT();
  const pubs = useQuery({ queryKey: ["publications"], queryFn: api.publications });
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-8">
      <p className="mb-1 text-xs uppercase tracking-wide text-muted">
        {ws.data ? `${t.workspace}: ${ws.data.label}` : ""}
      </p>
      <h1 className="mb-6 font-doc text-2xl font-semibold">{t.publications}</h1>
      {pubs.isLoading && <p className="text-muted">…</p>}
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
    </div>
  );
}
