import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { api, ApiRequestError, pautaPath, type IssueSummary } from "../api";
import { useT } from "../i18n";
import { fmtDate } from "../lib/fmt";
import { errText } from "../lib/errors";
import { Badge, Card } from "../ui";
import { NotFound } from "./ErrorPage";

// Exported formats read as product names — same in pt and en.
const FORMAT_LABEL: Record<string, string> = {
  linkedin: "LinkedIn",
  x: "X",
  html: "HTML",
  markdown: "Markdown",
};

// KLOX-184: a copied/downloaded pauta is "Exportada" even though its
// lifecycle status stays "approved" — the export mark lives beside it.
// KLOX-235: edits after that export read "Edited since export" until a
// fresh version is copied/downloaded.
const effStatus = (iss: IssueSummary) =>
  iss.stale_exports ? "edited" : iss.exported ? "exported" : iss.status;

export function PublicationPage() {
  const { id = "" } = useParams();
  const { t, lang } = useT();
  const pub = useQuery({ queryKey: ["publication", id], queryFn: () => api.publication(id) });
  const issues = useQuery({
    queryKey: ["issues", id],
    queryFn: () => api.issues(Number(id)),
    enabled: !!pub.data,
  });

  if (pub.isError) {
    if (pub.error instanceof ApiRequestError && pub.error.status === 404) return <NotFound />;
    return <p className="p-6 text-sm text-danger">{errText(t, pub.error)}</p>;
  }

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-6">
      <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-muted">{t.issues}</h2>
      <div className="grid grid-cols-1 gap-2">
        {issues.data?.map((iss) => {
          const st = effStatus(iss);
          return (
            <Link key={iss.id} to={pautaPath(iss.id, st)}>
              <Card className="flex items-center justify-between gap-3 hover:border-ink/40 transition-colors">
                <span className="min-w-0">
                  <span className="block truncate font-doc">
                    {iss.title || `${t.issue} ${iss.number}`}
                  </span>
                  <span className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted">
                    {iss.created_at && <span>{fmtDate(iss.created_at, lang)}</span>}
                    {(iss.exported_formats ?? []).map((f) => (
                      <span key={f} className="rounded-full bg-teal-soft px-1.5 py-0.5 text-[10px] font-medium text-teal">
                        {FORMAT_LABEL[f] ?? f}
                      </span>
                    ))}
                  </span>
                </span>
                <Badge tone={st === "exported" || st === "sent" ? "teal" : st === "approved" ? "ink" : st === "edited" ? "warn" : "muted"}>
                  {(t.status as Record<string, string>)[st] ?? st}
                </Badge>
              </Card>
            </Link>
          );
        })}
        {issues.data?.length === 0 && <p className="text-sm text-muted">{t.emptyIssues}</p>}
      </div>
    </div>
  );
}
