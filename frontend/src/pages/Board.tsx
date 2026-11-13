import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { GripVertical, Minus, Plus, Search } from "lucide-react";
import { api, ApiRequestError, type Candidate, type CandidatesResponse, type Dossier, type IssueSummary, type LineupEntry, type RunEvent } from "../api";
import { useT } from "../i18n";
import { cn } from "../lib/cn";
import { errText } from "../lib/errors";
import { Badge, Button, Card } from "../ui";
import { KDialog, KPopover } from "../ui-overlays";

// --- SSE step rail: real counts per finished step, never percentages ---

export function StepRail({ runID, steps, onDone }: { runID: string; steps: string[]; onDone: (run: { status: string; no_new_items?: boolean; error?: string }, events: RunEvent[]) => void }) {
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
  const countLabels = t.run.counts as Record<string, string | ((n: number) => string)>;
  return (
    <ol className="space-y-1.5">
      {steps.map((s) => {
        const ev = events.find((e) => e.step === s);
        const done = doneSteps.has(s);
        const bits = Object.entries(ev?.counts ?? {}).map(([k, v]) => {
          const lbl = countLabels[k] ?? k;
          return typeof lbl === "function" ? `${v} ${lbl(v)}` : `${v} ${lbl}`;
        });
        return (
          <li key={s} className="flex items-center gap-2 text-sm">
            <span
              className={cn(
                "h-2 w-2 shrink-0 rounded-full",
                done ? "bg-teal" : s === running ? "animate-pulse bg-ink" : "bg-line",
              )}
            />
            <span className={cn(done ? "text-ink" : "text-muted")}>{stepLabels[s] ?? s}</span>
            {bits.length > 0 && <span className="text-xs text-muted">— {bits.join(", ")}</span>}
          </li>
        );
      })}
    </ol>
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

type Board = { bench: Candidate[]; sections: Record<string, Candidate[]> };

// the board's i18n strings (readonly — they come from the translation table)
interface BoardStrings {
  add: string; remove: string; moveTo: string; full: string;
  openDossier: string; demoUnavailable: string; removeWhy: string;
  reasonChips: readonly string[]; reasonOther: string; justRemove: string;
  [k: string]: unknown;
}

function boardFrom(c: CandidatesResponse): Board {
  const sections: Record<string, Candidate[]> = {};
  for (const s of c.sections) sections[s.title] = [];
  const bench: Candidate[] = [];
  for (const cand of c.candidates) {
    if (cand.in_issue && cand.section && sections[cand.section]) {
      sections[cand.section].push(cand);
    } else {
      bench.push(cand);
    }
  }
  for (const k of Object.keys(sections)) sections[k].sort((a, b) => a.position - b.position);
  return { bench, sections };
}

function lineupOf(board: Board): LineupEntry[] {
  const out: LineupEntry[] = [];
  for (const [section, items] of Object.entries(board.sections)) {
    for (const c of items) out.push({ story_id: c.story_id, section });
  }
  return out;
}

// --- the build board ---

export function BuildBoard({ issue, onDrafted }: { issue: IssueSummary; onDrafted?: () => void }) {
  const { t } = useT();
  const qc = useQueryClient();
  const candsQ = useQuery({ queryKey: ["candidates", issue.id], queryFn: () => api.candidates(issue.id) });
  const [board, setBoard] = useState<Board | null>(null);
  const [toast, setToast] = useState<{
    text: string;
    undo?: { kind: "board"; board: Board } | { kind: "reject"; itemID: number };
    guide?: { text: string; url: string };
  } | null>(null);
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [search, setSearch] = useState("");
  const [sectionFilter, setSectionFilter] = useState("");
  const [sheet, setSheet] = useState<Candidate | null>(null);
  const [swapFor, setSwapFor] = useState<{ story: Candidate; section: string } | null>(null);
  const [focusKey, setFocusKey] = useState<string | null>(null);
  const [draftRun, setDraftRun] = useState<string | null>(null);
  const [boardTab, setBoardTab] = useState<"bench" | "issue">("bench");

  useEffect(() => {
    if (candsQ.data) setBoard(boardFrom(candsQ.data));
  }, [candsQ.data]);

  const showToast = useCallback(
    (
      text: string,
      undo?: { kind: "board"; board: Board } | { kind: "reject"; itemID: number },
      guide?: { text: string; url: string },
    ) => {
      if (toastTimer.current) clearTimeout(toastTimer.current);
      setToast({ text, undo, guide });
      toastTimer.current = setTimeout(() => setToast(null), 8000);
    },
    [],
  );

  // a pending Undo dies with the context it belonged to
  useEffect(() => { setToast(null); }, [issue.status]);

  const putLineup = useMutation({
    mutationFn: (v: { entries: LineupEntry[]; prev?: Board }) => api.lineup(issue.id, v.entries),
    onSuccess: (r) => {
      qc.setQueryData(["candidates", issue.id], (old: CandidatesResponse | undefined) =>
        old ? { ...old, candidates: r.candidates } : old,
      );
    },
    onError: (e, v) => {
      if (v.prev) setBoard(v.prev);
      if (e instanceof ApiRequestError && e.body.code === "section_full") {
        showToast(e.body.message);
      } else {
        showToast(errText(t, e));
      }
    },
  });

  const decide = useMutation({
    mutationFn: ({ itemID, reason }: { itemID: number; reason: string }) => api.decide(issue.id, itemID, "reject", reason),
    onError: (e) => showToast(errText(t, e)),
  });

  const applyBoard = useCallback(
    (next: Board, toastText: string) => {
      const snapshot = board!;
      setBoard(next);
      putLineup.mutate({ entries: lineupOf(next), prev: snapshot });
      showToast(toastText, { kind: "board", board: snapshot });
    },
    [board, putLineup, showToast],
  );

  const findCard = (storyID: number): { cand: Candidate; where: "bench" | string; idx: number } | null => {
    if (!board) return null;
    const bi = board.bench.findIndex((c) => c.story_id === storyID);
    if (bi >= 0) return { cand: board.bench[bi], where: "bench", idx: bi };
    for (const [sec, items] of Object.entries(board.sections)) {
      const i = items.findIndex((c) => c.story_id === storyID);
      if (i >= 0) return { cand: items[i], where: sec, idx: i };
    }
    return null;
  };

  const removeFrom = (b: Board, storyID: number): { b: Board; cand: Candidate } => {
    const nb: Board = { bench: [...b.bench], sections: { ...b.sections } };
    for (const [sec, items] of Object.entries(b.sections)) {
      const i = items.findIndex((c) => c.story_id === storyID);
      if (i >= 0) {
        const cand = items[i];
        nb.sections[sec] = items.filter((_, j) => j !== i);
        return { b: nb, cand };
      }
    }
    const i = b.bench.findIndex((c) => c.story_id === storyID);
    nb.bench = b.bench.filter((_, j) => j !== i);
    return { b: nb, cand: b.bench[i] };
  };

  const insertTo = (b: Board, cand: Candidate, section: string, idx?: number): Board => {
    const items = [...b.sections[section]];
    const at = idx === undefined ? items.length : Math.min(idx, items.length);
    items.splice(at, 0, { ...cand, section });
    return { ...b, sections: { ...b.sections, [section]: items } };
  };

  const toBench = (b: Board, cand: Candidate): Board => {
    const bench = [...b.bench, cand];
    bench.sort((a, c) => a.rank - c.rank);
    return { ...b, bench };
  };

  const sectionFull = (section: string) => {
    if (!board || !candsQ.data) return false;
    const cap = candsQ.data.sections.find((s) => s.title === section)?.capacity ?? 99;
    return board.sections[section].length >= cap;
  };

  // move a story to a section; when the section is full, offer the swap
  const moveTo = (storyID: number, section: string, idx?: number) => {
    if (!board) return;
    const loc = findCard(storyID);
    if (!loc) return;
    if (loc.where === section && idx === undefined) return;
    if (loc.where !== section && sectionFull(section) && !board.sections[section].some((c) => c.story_id === storyID)) {
      setSwapFor({ story: loc.cand, section });
      return;
    }
    let nb = removeFrom(board, storyID).b;
    const cand = { ...loc.cand, section };
    nb = insertTo(nb, cand, section, idx);
    const verb = loc.where === "bench" ? t.board.added : t.board.moved;
    setSwapFor(null);
    applyBoard(nb, verb(short(cand.title)));
  };

  const removeToBench = (storyID: number) => {
    if (!board) return;
    const loc = findCard(storyID);
    if (!loc || loc.where === "bench") return;
    const { b, cand } = removeFrom(board, storyID);
    applyBoard(toBench(b, cand), t.board.removed(short(cand.title)));
  };

  const swapWith = (storyID: number, section: string, outStoryID: number) => {
    if (!board) return;
    const loc = findCard(outStoryID);
    const { b, cand: out } = removeFrom(board, outStoryID);
    let nb = removeFrom(b, storyID).b;
    const inCand = { ...findCard(storyID)!.cand, section };
    nb = insertTo(nb, inCand, section, loc?.idx);
    nb = toBench(nb, out);
    setSwapFor(null);
    applyBoard(nb, t.board.moved(short(inCand.title)));
  };

  const rejectWith = (cand: Candidate, reason?: string) => {
    if (reason && cand.item_id && board) {
      // reason-fed removal: rejection, and the guide learns
      const loc = findCard(cand.story_id);
      if (loc) {
        const { b } = removeFrom(board, cand.story_id);
        setBoard(toBench(b, cand));
      }
      decide.mutate(
        { itemID: cand.item_id, reason },
        {
          onSuccess: (r) => {
            const added = r.guide?.added ?? [];
            const rule = added[0]?.replace(/^-\s*/, "");
            showToast(
              rule ? t.board.ruleLearned(rule) : t.board.removed(short(cand.title)),
              { kind: "reject", itemID: cand.item_id! },
              rule ? { text: t.board.viewGuide, url: `/publications/${issue.publication_id}/guide` } : undefined,
            );
            qc.invalidateQueries({ queryKey: ["candidates", issue.id] });
          },
        },
      );
    } else {
      removeToBench(cand.story_id);
    }
  };

  const undo = () => {
    const u = toast?.undo;
    if (!u) return;
    if (u.kind === "board") {
      setBoard(u.board);
      putLineup.mutate({ entries: lineupOf(u.board) });
    } else {
      api.decide(issue.id, u.itemID, "undo").then(() => qc.invalidateQueries({ queryKey: ["candidates", issue.id] }));
    }
    setToast(null);
    showToast(t.board.undone);
  };

  // --- drag and drop (desktop shortcut; buttons/keyboard do the same) ---
  const dragRef = useRef<number | null>(null);
  const [dropZone, setDropZone] = useState<string | null>(null);
  const onDragStart = (storyID: number) => (e: React.DragEvent) => {
    dragRef.current = storyID;
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", String(storyID));
  };
  const onDrop = (section: string, idx?: number) => (e: React.DragEvent) => {
    e.preventDefault();
    setDropZone(null);
    const sid = dragRef.current ?? Number(e.dataTransfer.getData("text/plain"));
    if (sid) moveTo(sid, section, idx);
    dragRef.current = null;
  };
  const onDropBench = (e: React.DragEvent) => {
    e.preventDefault();
    setDropZone(null);
    const sid = dragRef.current ?? Number(e.dataTransfer.getData("text/plain"));
    if (sid) removeToBench(sid);
    dragRef.current = null;
  };
  const allow = (zone: string) => (e: React.DragEvent) => {
    e.preventDefault();
    setDropZone(zone);
  };

  // --- keyboard: J/K navigate, Enter add/remove, 1-3 pick section ---
  const flatCards = useMemo(() => {
    if (!board) return [] as { key: string; cand: Candidate; where: string }[];
    const out: { key: string; cand: Candidate; where: string }[] = [];
    for (const c of benchFiltered(board.bench, search, sectionFilter)) {
      if (c.available) out.push({ key: `b${c.story_id}`, cand: c, where: "bench" });
    }
    for (const [sec, items] of Object.entries(board.sections)) {
      for (const c of items) out.push({ key: `s${c.story_id}`, cand: c, where: sec });
    }
    return out;
  }, [board, search, sectionFilter]);

  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).closest("input, textarea, [contenteditable], [role='dialog']")) return;
      const i = flatCards.findIndex((f) => f.key === focusKey);
      if (e.key === "j" || e.key === "k") {
        const n = flatCards.length;
        if (!n) return;
        const next = i < 0 ? 0 : (i + (e.key === "j" ? 1 : n - 1)) % n;
        setFocusKey(flatCards[next].key);
        e.preventDefault();
      } else if (e.key === "Enter" && i >= 0) {
        const f = flatCards[i];
        if (f.where === "bench") moveTo(f.cand.story_id, f.cand.suggested_section || firstOpenSection() || "");
        else removeToBench(f.cand.story_id);
        e.preventDefault();
      } else if (/^[1-9]$/.test(e.key) && i >= 0) {
        const sec = candsQ.data?.sections[Number(e.key) - 1];
        if (sec) {
          const f = flatCards[i];
          if (f.where === "bench") moveTo(f.cand.story_id, sec.title);
          else moveTo(f.cand.story_id, sec.title);
        }
        e.preventDefault();
      }
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  });

  const firstOpenSection = () => candsQ.data?.sections.find((s) => !sectionFull(s.title))?.title;

  const draftMut = useMutation({
    mutationFn: () => api.draft(issue.id),
    onSuccess: (run) => setDraftRun(run.id),
    onError: (e) => showToast(errText(t, e)),
  });

  if (!board || !candsQ.data) return <BoardSkeleton />;
  const sections = candsQ.data.sections;
  const total = sections.reduce((n, s) => n + board.sections[s.title].length, 0);
  const draftReady = sections.every((s) => board.sections[s.title].length > 0);
  const bench = benchFiltered(board.bench, search, sectionFilter);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* draft run: rail on top, the lineup stays visible underneath */}
      {draftRun && (
        <div className="border-b border-line bg-paper px-4 py-3">
          <p className="mb-2 text-sm font-semibold">{t.board.drafting}</p>
          <StepRail
            runID={draftRun}
            steps={["dossier", "draft", "citecheck"]}
            onDone={() => {
              setDraftRun(null);
              qc.invalidateQueries();
              onDrafted?.();
            }}
          />
        </div>
      )}

      {/* mobile column tabs */}
      <div className="flex gap-1 border-b border-line px-3 py-2 md:hidden">
        {(["bench", "issue"] as const).map((tab) => (
          <button
            key={tab}
            onClick={() => setBoardTab(tab)}
            className={cn(
              "rounded-[var(--radius-btn)] px-3 py-1.5 text-xs font-medium",
              boardTab === tab ? "bg-ink text-paper" : "text-muted",
            )}
          >
            {tab === "bench" ? t.board.bench : t.board.issue}
            {tab === "issue" ? ` (${total})` : ` (${board.bench.length})`}
          </button>
        ))}
      </div>

      <div className={cn("grid min-h-0 flex-1 grid-cols-1 md:grid-cols-2", draftRun && "pointer-events-none opacity-60")}>
        {/* left: ranked bench */}
        <section
          className={cn("min-h-0 flex-col overflow-y-auto border-line p-4 md:flex md:border-r", boardTab === "bench" ? "flex" : "hidden md:flex")}
          onDragOver={allow("bench")}
          onDragLeave={() => setDropZone(null)}
          onDrop={onDropBench}
          aria-label={t.board.bench}
        >
          <div className="mb-3 flex items-center gap-2">
            <h2 className="text-sm font-semibold">{t.board.bench}</h2>
            <span className="flex-1" />
            <div className="relative">
              <Search size={13} className="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-muted" />
              <input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t.board.search}
                className="h-7 w-40 rounded-[var(--radius-input)] border border-line bg-paper pl-7 pr-2 text-xs"
              />
            </div>
            <select
              value={sectionFilter}
              onChange={(e) => setSectionFilter(e.target.value)}
              className="h-7 rounded-[var(--radius-input)] border border-line bg-paper px-1 text-xs"
              aria-label={t.board.allSections}
            >
              <option value="">{t.board.allSections}</option>
              {sections.map((s) => (
                <option key={s.title} value={s.title}>{s.title}</option>
              ))}
            </select>
          </div>
          <div className={cn("space-y-2", dropZone === "bench" && "rounded-lg outline-dashed outline-2 outline-teal")}>
            {bench.length === 0 && <p className="py-6 text-center text-xs text-muted">{t.board.emptyBench}</p>}
            {bench.map((c, bi) => (
              <StoryCard
                key={c.story_id}
                c={c}
                board={t.board}
                side="bench"
                delay={Math.min(bi * 40, 480)}
                focused={focusKey === `b${c.story_id}`}
                onFocus={() => setFocusKey(`b${c.story_id}`)}
                onDragStart={onDragStart(c.story_id)}
                onOpen={() => setSheet(c)}
                onAdd={() =>
                  c.suggested_section && !sectionFull(c.suggested_section)
                    ? moveTo(c.story_id, c.suggested_section)
                    : setSwapFor({ story: c, section: "" })
                }
                onMoveTo={(sec) => moveTo(c.story_id, sec)}
                sections={sections}
                sectionFull={sectionFull}
              />
            ))}
          </div>
        </section>

        {/* right: the issue's section slots */}
        <section
          className={cn("min-h-0 flex-col overflow-y-auto p-4 md:flex", boardTab === "issue" ? "flex" : "hidden md:flex")}
          aria-label={t.board.issue}
        >
          <div className="sticky -top-4 z-10 -mx-4 -mt-4 mb-3 flex items-center gap-2 border-b border-line bg-paper px-4 pb-3 pt-4">
            <h2 className="text-sm font-semibold">{t.board.issue}</h2>
            <span className="flex-1" />
            <Button variant="teal" size="sm" disabled={!draftReady || draftMut.isPending || !!draftRun} onClick={() => draftMut.mutate()}>
              {t.board.draftAction(total)}
            </Button>
          </div>
          {sections.map((s, si) => {
            const items = board.sections[s.title];
            const full = sectionFull(s.title);
            return (
              <div
                key={s.title}
                data-section={s.title}
                className={cn("mb-4", dropZone === `sec:${s.title}` && "rounded-lg outline-dashed outline-2 outline-teal")}
                onDragOver={allow(`sec:${s.title}`)}
                onDragLeave={() => setDropZone(null)}
                onDrop={onDrop(s.title)}
              >
                <div className="mb-1.5 flex items-baseline gap-2 px-1">
                  <h3 className="text-xs font-semibold uppercase tracking-wide text-ink">{s.title}</h3>
                  <span className={cn("text-[11px]", full ? "text-warn" : "text-muted")}>
                    {items.length}/{s.capacity}
                    {full ? ` · ${t.board.full}` : ""}
                  </span>
                  <span className="text-[10px] text-muted">[{si + 1}]</span>
                </div>
                <div className="space-y-2">
                  {items.map((c, i) => (
                    <div
                      key={c.story_id}
                      className="flex items-start gap-2"
                      onDragOver={allow(`sec:${s.title}:${i}`)}
                      onDrop={onDrop(s.title, i)}
                    >
                      <span className="w-5 shrink-0 pt-3 text-right text-xs text-muted">{i + 1}.</span>
                      <div className="min-w-0 flex-1">
                        <StoryCard
                          c={c}
                          board={t.board}
                          side="issue"
                          lead={si === 0 && i === 0}
                          focused={focusKey === `s${c.story_id}`}
                          onFocus={() => setFocusKey(`s${c.story_id}`)}
                          onDragStart={onDragStart(c.story_id)}
                          onOpen={() => setSheet(c)}
                          onRemove={() => removeToBench(c.story_id)}
                          onReject={(reason) => rejectWith(c, reason)}
                          onMoveTo={(sec) => moveTo(c.story_id, sec)}
                          sections={sections}
                          sectionFull={sectionFull}
                        />
                      </div>
                    </div>
                  ))}
                  {Array.from({ length: s.capacity - items.length }, (_, k) => (
                    <div
                      key={`empty-${k}`}
                      className="flex items-start gap-2"
                      onDragOver={allow(`sec:${s.title}:${items.length + k}`)}
                      onDrop={onDrop(s.title, items.length + k)}
                    >
                      <span className="w-5 shrink-0 pt-3 text-right text-xs text-muted/50">{items.length + k + 1}.</span>
                      <div className="flex-1 rounded-[var(--radius-card)] border border-dashed border-line px-4 py-3 text-center text-xs text-muted">
                        {t.board.emptySlot(s.capacity - items.length)}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            );
          })}
          <p className="mt-auto pt-3 text-[11px] text-muted">{t.board.keyboard}</p>
        </section>
      </div>

      {/* swap chooser: adding to a full section picks what goes out */}
      <KDialog open={!!swapFor} onOpenChange={() => setSwapFor(null)} title={swapFor?.section || t.board.add}>
        {swapFor && !swapFor.section && (
          <div className="space-y-1">
            {sections.map((s) => (
              <button
                key={s.title}
                className="block w-full rounded-[var(--radius-btn)] border border-line px-3 py-2 text-left text-sm hover:bg-paper"
                onClick={() => moveTo(swapFor.story.story_id, s.title)}
              >
                {s.title}
                <span className="ml-2 text-xs text-muted">
                  {board.sections[s.title].length}/{s.capacity}
                  {sectionFull(s.title) ? ` · ${t.board.full}` : ""}
                </span>
              </button>
            ))}
          </div>
        )}
        {swapFor && swapFor.section && (
          <>
            <p className="mb-3 text-sm text-muted">{t.board.sectionFull(swapFor.section, board.sections[swapFor.section].length)}</p>
            <div className="space-y-1">
              {board.sections[swapFor.section].map((c) => (
                <button
                  key={c.story_id}
                  className="block w-full rounded-[var(--radius-btn)] border border-line px-3 py-2 text-left text-sm hover:bg-paper"
                  onClick={() => swapWith(swapFor.story.story_id, swapFor.section, c.story_id)}
                >
                  {c.title}
                </button>
              ))}
            </div>
          </>
        )}
      </KDialog>

      {/* dossier / story side sheet */}
      <StorySheet cand={sheet} onClose={() => setSheet(null)} />

      {/* toast */}
      {toast && (
        <div className="fixed bottom-4 left-1/2 z-50 flex -translate-x-1/2 items-center gap-3 rounded-full border border-line bg-ink px-4 py-2 text-sm text-paper shadow-lg">
          <span className="max-w-[60vw] truncate" title={toast.text}>{toast.text}</span>
          {toast.guide && (
            <a className="font-medium text-teal-soft underline" href={toast.guide.url}>
              {toast.guide.text}
            </a>
          )}
          {toast.undo && (
            <button className="font-medium text-teal-soft underline" onClick={undo}>
              {t.board.undo}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function benchFiltered(bench: Candidate[], q: string, section: string): Candidate[] {
  const needle = q.trim().toLowerCase();
  return bench.filter((c) => {
    if (section && c.suggested_section !== section && c.section !== section) return false;
    if (needle && !`${c.title} ${c.outlets.join(" ")} ${c.reason}`.toLowerCase().includes(needle)) return false;
    return true;
  });
}

// --- run landing: "Prepare" opens the board straight away in loading state ---

export function RunLanding() {
  const { runID } = useParams();
  const { t } = useT();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [done, setDone] = useState<{ no_new_items?: boolean; error?: string } | null>(null);
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
    if (issueID) nav(`/issues/${issueID}?run=${runID}`, { replace: true });
  }, [issueID, nav, runID]);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex items-center gap-3 border-b border-line px-4 py-2">
        <Link to="/" className="text-muted hover:text-ink">←</Link>
        <h1 className="text-lg font-semibold">{t.board.title}</h1>
        <Badge tone="warn">{(t.status as Record<string, string>).reviewing ?? "reviewing"}</Badge>
      </header>
      {done?.no_new_items || done?.error ? (
        <div className="flex flex-1 items-center justify-center p-8">
          <Card className="max-w-md p-6 text-center">
            <p className="text-sm">{done.error ? done.error : t.run.noNewItems}</p>
            {ws.data?.mode === "demo" && (
              <Button className="mt-4" variant="outline" size="sm" disabled={reset.isPending} onClick={() => reset.mutate()}>
                {reset.isPending ? "…" : t.demo.reset}
              </Button>
            )}
            <Link to="/" className="mt-4 block text-sm text-teal underline">
              ← {t.publications}
            </Link>
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

function short(title: string) {
  return title.length > 60 ? title.slice(0, 59) + "…" : title;
}

// --- one story card, shared by both columns ---

function StoryCard({
  c,
  board,
  side,
  focused,
  delay,
  lead,
  onFocus,
  onDragStart,
  onOpen,
  onAdd,
  onRemove,
  onReject,
  onMoveTo,
  sections,
  sectionFull,
}: {
  c: Candidate;
  board: BoardStrings;
  side: "bench" | "issue";
  focused: boolean;
  delay?: number;
  lead?: boolean;
  onFocus: () => void;
  onDragStart: (e: React.DragEvent) => void;
  onOpen: () => void;
  onAdd?: () => void;
  onRemove?: () => void;
  onReject?: (reason?: string) => void;
  onMoveTo: (section: string) => void;
  sections: { title: string; capacity: number }[];
  sectionFull: (s: string) => boolean;
}) {
  const [otherReason, setOtherReason] = useState("");
  const needsReasonOffer = side === "issue" && c.origin === "phaseA" && !!c.item_id;
  return (
    <Card
      className={cn(
        "card-in group p-3",
        lead && "border-ink/30 p-4",
        focused && "ring-2 ring-teal",
        !c.available && "opacity-60",
      )}
      style={delay ? { animationDelay: `${delay}ms` } : undefined}
      onFocus={onFocus}
      tabIndex={0}
      role="button"
      aria-label={`${c.title} — ${board[side === "bench" ? "add" : "remove"] as string}`}
      onKeyDown={(e) => {
        if (e.key === "Enter" && (e.target as HTMLElement).tagName !== "BUTTON") onOpen();
      }}
    >
      <div className="flex items-start gap-2">
        <span
          draggable={c.available}
          onDragStart={onDragStart}
          className="mt-1 hidden cursor-grab text-muted/60 hover:text-muted md:block"
          aria-hidden="true"
        >
          <GripVertical size={14} />
        </span>
        <button className="min-w-0 flex-1 text-left" onClick={onOpen}>
          <p className={cn("font-doc text-sm font-semibold leading-5", lead && "text-base leading-6")}>{c.title}</p>
          <p className="mt-0.5 text-xs text-muted">
            {c.outlets.join(", ")}
            {c.duplicates > 0 ? ` · +${c.duplicates}` : ""} · {c.score.toFixed(1)}/10
          </p>
          {c.reason && <p className="mt-0.5 line-clamp-2 text-xs text-muted">{c.reason}</p>}
          {!c.available && (
            <Badge tone="warn" className="mt-1">{board.demoUnavailable}</Badge>
          )}
        </button>
        <div className="flex shrink-0 flex-col items-end gap-1">
          {side === "bench" && c.available && (
            <Button size="sm" variant="outline" className="h-7 px-2" onClick={onAdd} aria-label={board.add}>
              <Plus size={13} />
            </Button>
          )}
          {side === "issue" && (
            <>
              {needsReasonOffer ? (
                <RemoveMenu board={board} onReject={onReject!} otherReason={otherReason} setOtherReason={setOtherReason} />
              ) : (
                <Button size="sm" variant="outline" className="h-7 px-2" onClick={onRemove} aria-label={board.remove}>
                  <Minus size={13} />
                </Button>
              )}
              <MoveMenu board={board} sections={sections} sectionFull={sectionFull} onMoveTo={onMoveTo} exclude={c.section} />
            </>
          )}
        </div>
      </div>
    </Card>
  );
}


// "move to section" menu — sections marked when full
function MoveMenu({
  board, sections, sectionFull, onMoveTo, exclude,
}: {
  board: Pick<BoardStrings, "moveTo" | "full">;
  sections: { title: string; capacity: number }[];
  sectionFull: (s: string) => boolean;
  onMoveTo: (s: string) => void;
  exclude?: string;
}) {
  return (
    <KPopover
      trigger={
        <Button size="sm" variant="ghost" className="h-7 px-2 text-xs" aria-label={board.moveTo}>
          {board.moveTo}
        </Button>
      }
    >
      <div className="space-y-1">
        {sections.map((s) => {
          const full = sectionFull(s.title);
          const same = s.title === exclude;
          return (
            <button
              key={s.title}
              disabled={same}
              className={cn(
                "block w-full rounded-[var(--radius-btn)] px-2 py-1.5 text-left text-sm",
                same ? "text-muted/50" : full ? "text-warn hover:bg-warn-soft" : "hover:bg-paper",
              )}
              onClick={() => onMoveTo(s.title)}
            >
              {s.title}
              {full && !same && <span className="ml-2 text-xs">· {board.full}</span>}
            </button>
          );
        })}
      </div>
    </KPopover>
  );
}

// remove menu: optional reason chips (they feed the guide) or plain remove
function RemoveMenu({
  board, onReject, otherReason, setOtherReason,
}: {
  board: Pick<BoardStrings, "remove" | "removeWhy" | "reasonChips" | "reasonOther" | "justRemove">;
  onReject: (reason?: string) => void;
  otherReason: string;
  setOtherReason: (v: string) => void;
}) {
  const chips = board.reasonChips;
  return (
    <KPopover
      trigger={
        <Button size="sm" variant="outline" className="h-7 px-2" aria-label={board.remove}>
          <Minus size={13} />
        </Button>
      }
    >
      <p className="mb-2 text-xs text-muted">{board.removeWhy}</p>
      <div className="flex flex-wrap gap-1">
        {chips.map((chip) => (
          <button
            key={chip}
            className="rounded-full border border-line px-2.5 py-1 text-xs hover:border-danger hover:text-danger"
            onClick={() => onReject(chip)}
          >
            {chip}
          </button>
        ))}
      </div>
      <div className="mt-2 flex gap-1">
        <input
          value={otherReason}
          onChange={(e) => setOtherReason(e.target.value)}
          placeholder={board.reasonOther}
          className="h-7 min-w-0 flex-1 rounded-[var(--radius-input)] border border-line bg-paper px-2 text-xs"
        />
        <Button size="sm" variant="danger" className="h-7" disabled={!otherReason.trim()} onClick={() => onReject(otherReason.trim())}>
          {board.remove}
        </Button>
      </div>
      <button className="mt-2 text-xs text-muted underline" onClick={() => onReject(undefined)}>
        {board.justRemove}
      </button>
    </KPopover>
  );
}

// side sheet: dossier when one exists, story detail otherwise
function StorySheet({ cand, onClose }: { cand: Candidate | null; onClose: () => void }) {
  const { t } = useT();
  const dossierQ = useQuery({
    queryKey: ["dossier", cand?.item_id],
    queryFn: () => api.dossier(cand!.item_id!),
    enabled: !!cand && !!cand.dossier && !!cand.item_id,
  });
  const storyQ = useQuery({
    queryKey: ["story", cand?.story_id],
    queryFn: () => api.story(cand!.story_id),
    enabled: !!cand && !(!!cand.dossier && !!cand.item_id),
  });
  return (
    <KDialog open={!!cand} onOpenChange={() => onClose()} title={cand?.title ?? t.board.sheet} wide>
      {cand?.dossier && dossierQ.data ? (
        <DossierView d={dossierQ.data.dossier} />
      ) : storyQ.data ? (
        <div className="space-y-3">
          <p className="text-xs text-muted">
            {storyQ.data.outlets.join(", ")} · {storyQ.data.score.toFixed(1)}/10 — {storyQ.data.reason}
          </p>
          {cand && !cand.dossier && <p className="text-xs italic text-muted">{t.board.noDossier}</p>}
          {storyQ.data.items.map((it) => (
            <div key={it.id} className="rounded-[var(--radius-card)] border border-line p-3">
              <a href={it.url} target="_blank" rel="noreferrer" className="text-sm font-medium text-teal underline">
                {it.title}
              </a>
              <p className="mt-0.5 text-xs text-muted">{it.outlet}</p>
              <p className="mt-1 text-xs leading-5 text-muted">{it.snippet}</p>
            </div>
          ))}
        </div>
      ) : (
        <p className="text-sm text-muted">…</p>
      )}
    </KDialog>
  );
}

function DossierView({ d }: { d: Dossier }) {
  const { t } = useT();
  return (
    <div className="space-y-3 text-sm">
      {d.summary && <p className="font-doc leading-6">{d.summary}</p>}
      {d.facts.length > 0 && (
        <ul className="list-disc space-y-1 pl-5">
          {d.facts.map((f, i) => (
            <li key={i}>
              {f.text} <span className="text-muted">[{f.ref}]</span>
            </li>
          ))}
        </ul>
      )}
      {d.unknowns.length > 0 && (
        <div>
          <p className="mb-1 text-xs font-semibold uppercase text-muted">{t.evidence.unknowns}</p>
          <ul className="list-disc space-y-1 pl-5 text-muted">
            {d.unknowns.map((u, i) => (
              <li key={i}>{u}</li>
            ))}
          </ul>
        </div>
      )}
      {d.sources.length > 0 && (
        <p className="text-xs text-muted">
          {d.sources.map((s) => `[${s.ref}] ${s.name ?? s.url}`).join(" · ")}
        </p>
      )}
    </div>
  );
}
