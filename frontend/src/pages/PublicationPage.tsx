import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiRequestError } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Badge, Button, Card } from "../ui";
import { ShellPrimary } from "../components/ShellPortal";
import { NotFound } from "./ErrorPage";

export function PublicationPage() {
  const { id = "" } = useParams();
  const { t } = useT();
  const nav = useNavigate();
  const pub = useQuery({ queryKey: ["publication", id], queryFn: () => api.publication(id) });
  const issues = useQuery({
    queryKey: ["issues", id],
    queryFn: () => api.issues(Number(id)),
    enabled: !!pub.data,
  });
  const start = useMutation({
    mutationFn: () => api.startRun(Number(id)),
    // "Prepare" opens the build board straight away — the run landing page
    // shows it in loading state while SSE narrates the real steps.
    onSuccess: (r) => nav(`/runs/${r.id}`),
  });

  if (pub.isError) {
    if (pub.error instanceof ApiRequestError && pub.error.status === 404) return <NotFound />;
    return <p className="p-6 text-sm text-danger">{errText(t, pub.error)}</p>;
  }

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-6">
      <ShellPrimary>
        <Button size="sm" onClick={() => start.mutate()} disabled={start.isPending}>
          {start.isPending ? t.run.running : t.run.start}
        </Button>
      </ShellPrimary>

      {start.error && (
        <Card className="mb-4 border-danger/40 bg-danger-soft p-3 text-sm text-danger">
          {errText(t, start.error)}
        </Card>
      )}

      <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-muted">{t.issues}</h2>
      <div className="grid grid-cols-1 gap-2">
        {issues.data?.map((iss) => (
          <Link key={iss.id} to={`/issues/${iss.id}`}>
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
        {issues.data?.length === 0 && <p className="text-sm text-muted">—</p>}
      </div>
    </div>
  );
}
