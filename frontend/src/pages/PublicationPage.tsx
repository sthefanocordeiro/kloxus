import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { api, ApiRequestError, pautaPath } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Badge, Card } from "../ui";
import { NotFound } from "./ErrorPage";

export function PublicationPage() {
  const { id = "" } = useParams();
  const { t } = useT();
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
        {issues.data?.map((iss) => (
          <Link key={iss.id} to={pautaPath(iss.id, iss.status)}>
            <Card className="flex items-center justify-between hover:border-ink/40 transition-colors">
              <span className="font-doc">
                {t.issue} {iss.number}
              </span>
              <Badge tone={iss.status === "sent" ? "teal" : iss.status === "approved" ? "ink" : "muted"}>
                {(t.status as Record<string, string>)[iss.status] ?? iss.status}
              </Badge>
            </Card>
          </Link>
        ))}
        {issues.data?.length === 0 && <p className="text-sm text-muted">{t.emptyIssues}</p>}
      </div>
    </div>
  );
}
