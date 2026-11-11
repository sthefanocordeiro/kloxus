import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type Run } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Badge, Button, Card } from "../ui";

// RunProgress streams /runs/{id}/events (SSE) and narrates each finished
// step with its real counts — never percentages.
export function RunProgress({ runID, onDone }: { runID: string; onDone: (r: Run) => void }) {
  const { t } = useT();
  const [events, setEvents] = useState<{ seq: number; step: string; counts?: Record<string, number> }[]>([]);
  const [status, setStatus] = useState("running");
  const doneRef = useRef(false);

  useEffect(() => {
    const es = new EventSource(`/api/v1/runs/${runID}/events`);
    es.addEventListener("step", (e) => {
      const ev = JSON.parse((e as MessageEvent).data);
      setEvents((prev) => [...prev, ev]);
    });
    es.addEventListener("done", (e) => {
      const run = JSON.parse((e as MessageEvent).data) as Run;
      setStatus(run.status);
      es.close();
      if (!doneRef.current) {
        doneRef.current = true;
        onDone(run);
      }
    });
    es.onerror = () => {
      es.close();
      setStatus((s) => (s === "running" ? "failed" : s));
    };
    return () => es.close();
  }, [runID]);

  return (
    <Card className="text-sm">
      <ul className="space-y-1">
        {events.map((ev) => (
          <li key={ev.seq} className="flex justify-between gap-4">
            <span className="font-medium">
              {(t.run.step as Record<string, string>)[ev.step] ?? ev.step}
            </span>
            <span className="text-muted">
              {ev.counts &&
                Object.entries(ev.counts)
                  .map(([k, v]) => `${v} ${(t.run.counts as Record<string, string>)[k] ?? k}`)
                  .join(" · ")}
            </span>
          </li>
        ))}
        {status === "running" && <li className="text-muted">{t.run.running}</li>}
      </ul>
    </Card>
  );
}

export function PublicationPage() {
  const { id = "" } = useParams();
  const { t } = useT();
  const nav = useNavigate();
  const qc = useQueryClient();
  const pub = useQuery({ queryKey: ["publication", id], queryFn: () => api.publication(id) });
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });
  const issues = useQuery({
    queryKey: ["issues", id],
    queryFn: () => api.issues(Number(id)),
    enabled: !!pub.data,
  });
  const [runID, setRunID] = useState<string | null>(null);
  const [runMsg, setRunMsg] = useState<string | null>(null);
  const start = useMutation({
    mutationFn: () => api.startRun(Number(id)),
    onSuccess: (r) => {
      setRunMsg(null);
      setRunID(r.id);
    },
  });
  const reset = useMutation({
    mutationFn: api.demoReset,
    onSuccess: () => {
      setRunMsg(null);
      qc.clear();
    },
  });

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-8">
      <Link to="/" className="text-sm text-muted hover:text-ink">
        ← {t.back}
      </Link>
      <div className="mt-3 mb-6 flex items-baseline justify-between gap-4">
        <h1 className="font-doc text-2xl font-semibold">{pub.data?.publication.name ?? "…"}</h1>
        <Button onClick={() => start.mutate()} disabled={start.isPending || !!runID}>
          {t.run.start}
        </Button>
      </div>

      {start.error && (
        <Card className="mb-4 border-danger/40 bg-danger-soft p-3 text-sm text-danger">
          {errText(t, start.error)}
        </Card>
      )}

      {runMsg && (
        <Card className="mb-4 flex flex-wrap items-center justify-between gap-3 border-warn/40 bg-warn-soft p-3 text-sm">
          <span>{runMsg}</span>
          {ws.data?.mode === "demo" && (
            <Button variant="outline" size="sm" disabled={reset.isPending} onClick={() => reset.mutate()}>
              {reset.isPending ? "…" : t.demo.reset}
            </Button>
          )}
        </Card>
      )}

      {runID && (
        <div className="mb-6">
          <RunProgress
            runID={runID}
            onDone={(r) => {
              // clear before anything else — React keeps this component's
              // state when the user navigates back, and a stale runID
              // would leave the button disabled forever
              setRunID(null);
              qc.invalidateQueries({ queryKey: ["issues", id] });
              if (r.issue_id) nav(`/issues/${r.issue_id}`);
              // a finished run without an issue means the fixture had
              // nothing left — say so instead of looking broken
              else setRunMsg(r.error ?? t.run.noNewItems);
            }}
          />
        </div>
      )}

      <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-muted">{t.issues}</h2>
      <div className="grid gap-2">
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
