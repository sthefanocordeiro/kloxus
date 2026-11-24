import { useMutation, useQuery } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Navigate, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, pautaPath, type PautaEntry } from "../api";
import { BackButton } from "../components/BackButton";
import { DestinationStep } from "../components/Destinations";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Button, Card } from "../ui";
import { usePautaStrings } from "./pautaStrings";

// /issues/:id — the sentence editor is gone (KLOX-179); old links land
// on the pauta editor, straight to the export step once the pauta left.
export function IssueRedirect() {
  const { id = "" } = useParams();
  const q = useQuery({
    queryKey: ["issue", id],
    queryFn: () => api.issue(Number(id)),
    retry: false,
    staleTime: 60_000,
  });
  if (!q.data && !q.isError) return null;
  // an exported pauta opens straight on the export step (KLOX-184)
  return <Navigate to={pautaPath(id, q.data?.exported ? "exported" : q.data?.status)} replace />;
}

// /pauta/:id — Escrever (KLOX-168): the ticked news in the chosen order,
// an optional opening, and a "Comentar" button under each news that opens
// its single optional comment space (KLOX-162, autosaved). No sentences,
// citations or checks — the only gate is a heads-up about news left
// without text.
export function PautaPage() {
  const { id } = useParams();
  const issueID = Number(id);
  const { t } = useT();
  const s = usePautaStrings();
  const nav = useNavigate();
  const q = useQuery({ queryKey: ["pauta", issueID], queryFn: () => api.pauta(issueID), staleTime: Infinity });
  const issueQ = useQuery({ queryKey: ["issue", issueID], queryFn: () => api.issue(issueID) });
  const pubID = issueQ.data?.publication_id;

  const [opening, setOpening] = useState("");
  const [entries, setEntries] = useState<PautaEntry[]>([]);
  const [ready, setReady] = useState(false);
  const [saveState, setSaveState] = useState<"idle" | "saving" | "saved" | "error">("idle");
  const [adding, setAdding] = useState(false);
  const [openComments, setOpenComments] = useState<Set<number>>(new Set());
  const latest = useRef({ opening, entries });
  latest.current = { opening, entries };
  const dirty = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (q.data && !ready) {
      setOpening(q.data.opening);
      setEntries(q.data.entries);
      setReady(true);
    }
  }, [q.data, ready]);

  const persist = useCallback(async () => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = null;
    if (!dirty.current) return;
    dirty.current = false;
    setSaveState("saving");
    const cur = latest.current;
    try {
      await api.pautaSave(issueID, {
        opening: cur.opening,
        entries: cur.entries.map((e) => ({ story_id: e.story_id, text: e.text })),
      });
      setSaveState(dirty.current ? "saving" : "saved");
    } catch {
      dirty.current = true;
      setSaveState("error");
    }
  }, [issueID]);

  const touch = useCallback(() => {
    dirty.current = true;
    setSaveState("saving");
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => void persist(), 700);
  }, [persist]);

  // leaving the page never drops the last keystrokes
  useEffect(() => () => { void persist(); }, [persist]);

  const edit = (fn: (e: PautaEntry[]) => PautaEntry[]) => {
    setEntries((p) => fn(p));
    touch();
  };
  const move = (i: number, d: number) =>
    edit((p) => {
      const j = i + d;
      if (j < 0 || j >= p.length) return p;
      const n = [...p];
      [n[i], n[j]] = [n[j], n[i]];
      return n;
    });

  const finish = useMutation({
    mutationFn: async () => {
      await persist();
      return api.pautaFinish(issueID);
    },
    onSuccess: (r) => nav(`/pauta/${issueID}/export${r.missing_text > 0 ? `?sem_texto=${r.missing_text}` : ""}`),
  });

  const today = useQuery({
    queryKey: ["today", String(pubID)],
    queryFn: () => api.today(pubID!),
    enabled: adding && !!pubID,
  });
  const have = new Set(entries.map((e) => e.story_id));
  const options = (today.data?.cards ?? []).filter((c) => !have.has(c.story_id));

  if (q.isError) return <p className="p-6 text-sm text-danger" role="alert">{errText(t, q.error)}</p>;
  if (!ready) return <p className="p-6 text-sm text-muted" aria-busy="true">{s.loading}</p>;

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-6 pb-28">
      <BackButton to={pubID ? `/publications/${pubID}/today` : "/publications"} dest={s.todayDest} />
      <div className="mt-4 flex items-baseline justify-between gap-3">
        <h1 className="font-display text-2xl font-bold">{s.title}</h1>
        <span className="text-xs text-muted" role="status" aria-live="polite">
          {saveState === "saving" ? s.saving : saveState === "saved" ? s.saved : saveState === "error" ? s.saveError : ""}
        </span>
      </div>
      <p className="mt-1 text-sm text-muted">{s.hint}</p>

      <label className="mt-6 block text-sm font-medium" htmlFor="opening">{s.opening}</label>
      <textarea
        id="opening"
        className="mt-1 w-full rounded border border-line bg-paper-raised p-3 text-sm"
        rows={2}
        placeholder={s.openingHint}
        value={opening}
        onChange={(e) => { setOpening(e.target.value); touch(); }}
      />

      {entries.length === 0 && <p className="mt-6 text-sm text-muted">{s.empty}</p>}
      <ol className="mt-6 space-y-4">
        {entries.map((e, i) => (
          <li key={e.story_id}>
            <Card className="p-4" data-testid="pauta-entry">
              <div className="flex items-start gap-3">
                <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-full bg-teal text-sm text-white">{i + 1}</span>
                <div className="min-w-0 flex-1">
                  <h2 className="font-display text-lg font-semibold leading-snug">
                    {e.url ? (
                      <a href={e.url} target="_blank" rel="noreferrer noopener" className="underline decoration-line underline-offset-2 hover:decoration-ink">
                        {e.title}
                      </a>
                    ) : e.title}
                  </h2>
                  <p className="mt-0.5 text-xs text-muted">
                    {[e.outlet, e.date && e.date.slice(0, 10)].filter(Boolean).join(" · ")}
                  </p>
                </div>
                <div className="flex shrink-0 gap-1">
                  <Button size="icon" variant="ghost" aria-label={s.up} disabled={i === 0} onClick={() => move(i, -1)}><ArrowUp size={16} /></Button>
                  <Button size="icon" variant="ghost" aria-label={s.down} disabled={i === entries.length - 1} onClick={() => move(i, 1)}><ArrowDown size={16} /></Button>
                  <Button size="icon" variant="ghost" aria-label={`${s.remove}: ${e.title}`} onClick={() => edit((p) => p.filter((x) => x.story_id !== e.story_id))}><X size={16} /></Button>
                </div>
              </div>
              {openComments.has(e.story_id) || e.text !== "" ? (
                <textarea
                  className="mt-3 w-full rounded border border-line bg-paper p-3 text-sm"
                  rows={4}
                  aria-label={`${s.text}: ${e.title}`}
                  placeholder={s.textHint}
                  value={e.text}
                  onChange={(ev) => edit((p) => p.map((x) => (x.story_id === e.story_id ? { ...x, text: ev.target.value } : x)))}
                />
              ) : (
                <Button
                  size="sm"
                  variant="outline"
                  className="mt-3"
                  aria-label={`${s.comment}: ${e.title}`}
                  onClick={() => setOpenComments((p) => new Set(p).add(e.story_id))}
                >
                  {s.comment}
                </Button>
              )}
            </Card>
          </li>
        ))}
      </ol>

      <div className="mt-4">
        {!adding ? (
          <Button variant="outline" onClick={() => setAdding(true)}>{s.add}</Button>
        ) : (
          <Card className="p-4">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">{s.addTitle}</h2>
              <Button size="sm" variant="ghost" onClick={() => setAdding(false)}>{s.close}</Button>
            </div>
            {today.isLoading && <p className="mt-2 text-sm text-muted">{s.loading}</p>}
            {today.data && options.length === 0 && <p className="mt-2 text-sm text-muted">{s.noMore}</p>}
            <ul className="mt-2 divide-y divide-line">
              {options.map((c) => (
                <li key={c.story_id} className="flex items-center justify-between gap-3 py-2">
                  <span className="text-sm">
                    {c.title} <span className="text-xs text-muted">· {c.outlet}</span>
                  </span>
                  <Button
                    size="sm"
                    variant="outline"
                    aria-label={`${s.addOne}: ${c.title}`}
                    onClick={() => edit((p) => [...p, { story_id: c.story_id, title: c.title, url: c.url, outlet: c.outlet, date: "", text: "" }])}
                  >
                    {s.addOne}
                  </Button>
                </li>
              ))}
            </ul>
          </Card>
        )}
      </div>

      {/* fixed just above the global bottom nav on phone — at bottom-0
          the z-40 nav covered it and taps hit Configurações (KLOX-180) */}
      <div
        className="fixed inset-x-0 bottom-[calc(3.5rem+env(safe-area-inset-bottom))] z-20 border-t border-line bg-paper-raised/95 px-4 py-3 backdrop-blur md:bottom-0"
        role="region"
        aria-label={s.toExport}
      >
        <div className="mx-auto flex max-w-3xl items-center justify-between gap-3">
          {finish.isError ? (
            <p className="text-sm text-danger" role="alert">{errText(t, finish.error)}</p>
          ) : <span />}
          <Button disabled={finish.isPending || entries.length === 0} onClick={() => finish.mutate()}>
            {s.toExport}
          </Button>
        </div>
      </div>
    </div>
  );
}

// /pauta/:id/export — the existing destinations step on the pauta; the
// only notice is how many news items have no text (never a blocker).
export function PautaExport() {
  const { id } = useParams();
  const issueID = Number(id);
  const s = usePautaStrings();
  const [params] = useSearchParams();
  const missing = Number(params.get("sem_texto") ?? 0);
  const q = useQuery({ queryKey: ["pauta", issueID, "export"], queryFn: () => api.pauta(issueID) });
  const ready = useQuery({
    queryKey: ["pauta-finish", issueID],
    queryFn: () => api.pautaFinish(issueID),
    staleTime: Infinity,
  });
  return (
    <div className="mx-auto w-full max-w-4xl px-4 py-6">
      <BackButton to={`/pauta/${issueID}`} dest={s.title} />
      <h1 className="mt-4 font-display text-2xl font-bold">{s.exportTitle}</h1>
      {missing > 0 && (
        <p className="mt-3 rounded border border-warn/40 bg-warn-soft p-3 text-sm" role="status">
          {(missing === 1 ? s.missing1 : s.missingN).replace("{n}", String(missing))}
        </p>
      )}
      {q.data && ready.isSuccess && (
        <DestinationStep issueID={issueID} stories={q.data.entries.map((e) => ({ headline: e.title }))} />
      )}
    </div>
  );
}
