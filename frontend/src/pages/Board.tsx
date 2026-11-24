import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type Run, type RunEvent } from "../api";
import { useT } from "../i18n";

import { cn } from "../lib/cn";
import { isNoItemsFailure, runErrText } from "../lib/errors";
import { Badge, Button, Card } from "../ui";

// --- SSE step rail: real counts per finished step, never percentages ---

export function StepRail({ runID, steps, onDone }: { runID: string; steps: string[]; onDone: (run: Run, events: RunEvent[]) => void }) {
  const { t } = useT();
  const [events, setEvents] = useState<RunEvent[]>([]);
  const eventsRef = useRef<RunEvent[]>([]);
  const doneRef = useRef(false);
  // onDone is an inline prop (new identity every render) — keep it in a
  // ref so the SSE effect only keys on runID instead of reconnecting on
  // every parent render.
  const onDoneRef = useRef(onDone);
  onDoneRef.current = onDone;
  useEffect(() => {
    let poll: ReturnType<typeof setInterval> | undefined;
    const es = new EventSource(`/api/v1/runs/${runID}/events`);
    es.addEventListener("step", (e) => {
      const ev = JSON.parse((e as MessageEvent).data) as RunEvent;
      setEvents((prev) => (prev.some((p) => p.seq === ev.seq) ? prev : [...prev, ev]));
      if (!eventsRef.current.some((p) => p.seq === ev.seq)) eventsRef.current = [...eventsRef.current, ev];
    });
    es.addEventListener("done", (e) => {
      es.close();
      if (doneRef.current) return;
      doneRef.current = true;
      const run = JSON.parse((e as MessageEvent).data);
      onDoneRef.current(run, eventsRef.current);
    });
    // A real run outlives any proxy idle timeout — when the stream dies,
    // fall back to polling the run snapshot instead of stalling the rail.
    es.onerror = () => {
      es.close();
      poll ??= setInterval(async () => {
        try {
          const run = await api.run(runID);
          if (run.status !== "running") {
            clearInterval(poll);
            if (doneRef.current) return;
            doneRef.current = true;
            onDoneRef.current(run, eventsRef.current);
          }
        } catch {
          // transient — keep polling
        }
      }, 3000);
    };
    return () => {
      es.close();
      if (poll) clearInterval(poll);
    };
  }, [runID]);

  const doneSteps = new Set(events.map((e) => e.step));
  const running = steps.find((s) => !doneSteps.has(s)); // first unfinished step ticks
  const stepLabels = t.run.step as Record<string, string>;
  return (
    <ol className="space-y-1.5">
      {steps.map((s) => {
        const done = doneSteps.has(s);
        return (
          <li key={s} className="flex items-center gap-2 text-sm">
            <span
              className={cn(
                "h-2 w-2 shrink-0 rounded-full",
                done ? "bg-teal" : s === running ? "animate-pulse bg-ink" : "bg-line",
              )}
            />
            <span className={cn(done ? "text-ink" : "text-muted")}>{stepLabels[s] ?? s}</span>
          </li>
        );
      })}
    </ol>
  );
}

// BlockedCard is the guard-refused run view (D32): a clear creator-facing
// message — never a bare error.
export function BlockedCard({ message }: { message?: string }) {
  const { t } = useT();
  return (
    <div className="mx-auto w-full max-w-md space-y-4 p-6">
      <Card className="border-warn/50 p-5">
        <p className="text-sm font-semibold">{t.run.blockedTitle}</p>
        <p className="mt-1 text-sm text-muted">{message || t.run.blocked}</p>
      </Card>
    </div>
  );
}

// skeleton cards while the run works
export function BoardSkeleton() {
  return (
    <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 p-4 md:grid-cols-2">
      {[0, 1].map((col) => (
        <div key={col} className="space-y-3">
          <div className="h-5 w-32 animate-pulse rounded bg-line/60" />
          {[0, 1, 2, 3].map((i) => (
            <div key={i} className="animate-pulse rounded-[var(--radius-card)] border border-line bg-paper-raised p-4">
              <div className="h-4 w-3/4 rounded bg-line/60" />
              <div className="mt-2 h-3 w-1/2 rounded bg-line/40" />
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

// --- run landing: "Prepare" opens the board straight away in loading state ---

export function RunLanding() {
  const { runID } = useParams();
  const { t } = useT();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [done, setDone] = useState<Run | null>(null);
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace });
  const reset = useMutation({
    mutationFn: api.demoReset,
    onSuccess: () => {
      qc.clear();
      nav("/");
    },
  });

  // the server may take a moment to register the run row — retry briefly
  const runQ = useQuery({
    queryKey: ["run", runID],
    queryFn: () => api.run(runID!),
    enabled: !!runID,
    refetchInterval: 700,
    retry: 8,
    retryDelay: 400,
  });

  const issueID = runQ.data?.issue_id;
  useEffect(() => {
    if (issueID) nav(`/pauta/${issueID}`, { replace: true });
  }, [issueID, nav]);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex items-center gap-3 border-b border-line px-4 py-2">
        <h1 className="font-display text-lg font-bold">{t.board.title}</h1>
        <Badge tone="warn">{(t.status as Record<string, string>).reviewing ?? "reviewing"}</Badge>
      </header>
      {done?.blocked ? (
        <BlockedCard message={done.error} />
      ) : done?.no_new_items || done?.error ? (
        <div className="flex flex-1 items-center justify-center p-8">
          <Card className="max-w-md p-6 text-center">
            <p className="text-sm">{done.error ? runErrText(t, done.error) : t.run.noNewItems}</p>
            {ws.data?.mode === "demo" && (
              <Button className="mt-4" variant="outline" size="sm" disabled={reset.isPending} onClick={() => reset.mutate()}>
                {reset.isPending ? "…" : t.demo.reset}
              </Button>
            )}
            {/* the empty-harvest failure points at sources — that's the
                page the copy tells the creator to check (KLOX-90) */}
            {isNoItemsFailure(done.error) && done.publication_id ? (
              <Link
                to={`/publications/${done.publication_id}/sources`}
                className="mt-4 block text-sm text-teal underline"
              >
                {t.run.openSources}
              </Link>
            ) : (
              <Link to="/publications" className="mt-4 block text-sm text-teal underline">
                {t.publications}
              </Link>
            )}
          </Card>
        </div>
      ) : (
        <>
          <div className="mx-auto w-full max-w-md p-6">
            <h2 className="mb-4 text-lg font-semibold">{t.board.preparing}</h2>
            {runID && (
              <StepRail
                runID={runID}
                steps={["collect", "dedupe", "triage", "storylist", "dossier", "deepdive"]}
                onDone={(run) => setDone(run)}
              />
            )}
          </div>
          <BoardSkeleton />
        </>
      )}
    </div>
  );
}
