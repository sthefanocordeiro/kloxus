import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ThumbsDown, ThumbsUp } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiRequestError, pautaPath, type PautaCard, type PieceRow } from "../api";
import { useT } from "../i18n";
import { cn } from "../lib/cn";
import { fmtDate } from "../lib/fmt";
import { errText, isRateLimited } from "../lib/errors";
import { RateLimited } from "../components/RateLimited";
import { Badge, Button, Card, SkeletonLine } from "../ui";
import { useToday } from "./todayStrings";

// Exported formats read as product names — same in pt and en.
const FORMAT_LABEL: Record<string, string> = {
  linkedin: "LinkedIn",
  x: "X",
  html: "HTML",
  markdown: "Markdown",
};

// /publications/:id/today — Notícias de hoje (KLOX-168): the day's news
// for the radar. Clicking a card ticks it (KLOX-200) — the ticking order
// becomes the text order — and the thumbs keep training the radar.
// "Montar pauta →" opens the Escrever page. No model is called here.
export function Today() {
  const { id } = useParams();
  const { t, lang } = useT();
  const s = useToday();
  const nav = useNavigate();
  const qc = useQueryClient();
  const key = ["today", id];
  const q = useQuery({ queryKey: key, queryFn: () => api.today(id!), staleTime: 60_000 });
  // answered cards: thumbs-down leaves the list, thumbs-up stays marked
  const [gone, setGone] = useState<Set<number>>(new Set());
  const [liked, setLiked] = useState<Set<number>>(new Set());
  // story ids in the order they were ticked
  const [picked, setPicked] = useState<number[]>([]);
  // set when pautaCreate 404s: the deck was rebuilt and picks were
  // pruned — the user must hear about it (KLOX-197)
  const [stale, setStale] = useState(false);

  const answer = useMutation({
    mutationFn: (v: { c: PautaCard; a: "yes" | "no" }) => api.calibrationAnswer(id!, v.c.story_id, v.a),
    onSuccess: (_r, v) => {
      if (v.a === "no") {
        setGone((p) => new Set(p).add(v.c.story_id));
        setPicked((p) => p.filter((x) => x !== v.c.story_id));
      } else setLiked((p) => new Set(p).add(v.c.story_id));
    },
  });

  const build = useMutation({
    mutationFn: () => api.pautaCreate(id!, picked),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: key });
      nav(`/pauta/${r.issue_id}`);
    },
    onMutate: () => setStale(false),
    onError: async (err) => {
      // the deck was rebuilt since these cards were served: drop the
      // picks the new deck no longer has so a retry can't repeat the
      // same 404, and flag it — the error must not stay invisible
      if (!(err instanceof ApiRequestError && err.status === 404)) return;
      try {
        const fresh = await qc.fetchQuery({ queryKey: key, queryFn: () => api.today(id!) });
        const live = new Set(fresh.cards.map((c) => c.story_id));
        setPicked((p) => p.filter((x) => live.has(x)));
        setLiked((p) => new Set([...p].filter((x) => live.has(x))));
        setGone((p) => new Set([...p].filter((x) => live.has(x))));
      } finally {
        setStale(true);
      }
    },
  });

  const toggle = (sid: number) =>
    setPicked((p) => (p.includes(sid) ? p.filter((x) => x !== sid) : [...p, sid]));

  const cards = (q.data?.cards ?? []).filter((c) => !gone.has(c.story_id));
  const pieces = q.data?.pieces ?? [];
  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-6 pb-28">
      <h1 className="font-display text-2xl font-bold">{s.title}</h1>
      <p className="mt-1 text-sm text-muted">{s.hint}</p>

      {q.isLoading && (
        <Card className="mt-6 p-5" aria-busy="true">
          <p className="text-sm text-muted">{s.loading}</p>
          <SkeletonLine />
          <SkeletonLine />
        </Card>
      )}
      {q.isError &&
        (isRateLimited(q.error) ? (
          <RateLimited className="mt-6" error={q.error} busy={q.isFetching} onRetry={() => q.refetch()} />
        ) : (
          <p className="mt-6 text-sm text-danger" role="alert">{errText(t, q.error)}</p>
        ))}
      {stale && <p className="mt-4 text-sm text-danger" role="alert">{s.stale}</p>}
      {build.isError && !(build.error instanceof ApiRequestError && build.error.status === 404) &&
        (isRateLimited(build.error) ? (
          <RateLimited className="mt-4" error={build.error} busy={build.isPending} onRetry={() => build.mutate()} />
        ) : (
          <p className="mt-4 text-sm text-danger" role="alert">{errText(t, build.error)}</p>
        ))}

      {q.data && cards.length === 0 && (
        <Card className="mt-6 p-5">
          <p className="text-sm text-muted">{s.empty}</p>
        </Card>
      )}

      <ul className="mt-6 space-y-3" aria-label={s.title}>
        {cards.map((c) => {
          const pos = picked.indexOf(c.story_id);
          return (
            <li key={c.story_id}>
              <Card
                className={cn("cursor-pointer p-4", pos >= 0 && "border-teal")}
                data-testid="today-card"
                onClick={() => toggle(c.story_id)}
              >
                <div className="flex items-start gap-3">
                  <input
                    type="checkbox"
                    className="mt-1.5 h-5 w-5 shrink-0 cursor-pointer accent-teal"
                    aria-label={`${s.select}: ${c.title}`}
                    checked={pos >= 0}
                    onChange={() => toggle(c.story_id)}
                    onClick={(e) => e.stopPropagation()}
                  />
                  <div className="min-w-0 flex-1">
                    <h2 className="font-display text-lg font-semibold leading-snug">
                      {pos >= 0 && (
                        <span className="mr-2 inline-block rounded-full bg-teal px-2 text-sm text-white" data-testid="pick-order">
                          {pos + 1}
                        </span>
                      )}
                      {c.title}
                    </h2>
                    <p className="mt-1 text-xs text-muted">
                      {c.outlet}
                      {c.outlets > 1 && ` · ${s.outlets.replace("{n}", String(c.outlets))}`}
                      {c.age_hours !== null && ` · ${s.hoursAgo.replace("{n}", String(Math.round(c.age_hours)))}`}
                    </p>
                    {c.summary && <p className="mt-2 text-sm">{c.summary}</p>}
                  </div>
                </div>
                {/* KLOX-200: Sim/Não became small thumbs — they only
                    train the radar, they do not tick the card */}
                <div className="mt-3 flex items-center gap-2 pl-8">
                  <Button
                    size="icon"
                    variant="outline"
                    title={s.more}
                    aria-label={`${s.more}: ${c.title}`}
                    aria-pressed={liked.has(c.story_id)}
                    disabled={answer.isPending || liked.has(c.story_id)}
                    className={cn(liked.has(c.story_id) && "bg-teal-soft text-teal")}
                    onClick={(e) => {
                      e.stopPropagation();
                      answer.mutate({ c, a: "yes" });
                    }}
                  >
                    <ThumbsUp className="h-4 w-4" />
                  </Button>
                  <Button
                    size="icon"
                    variant="outline"
                    title={s.less}
                    aria-label={`${s.less}: ${c.title}`}
                    disabled={answer.isPending}
                    onClick={(e) => {
                      e.stopPropagation();
                      answer.mutate({ c, a: "no" });
                    }}
                  >
                    <ThumbsDown className="h-4 w-4" />
                  </Button>
                </div>
              </Card>
            </li>
          );
        })}
      </ul>

      <h2 className="mt-10 font-display text-xl font-bold">{s.piecesTitle}</h2>
      {q.data && pieces.length === 0 && <p className="mt-2 text-sm text-muted">{s.noPieces}</p>}
      <ul className="mt-3 divide-y divide-line rounded border border-line bg-paper-raised">
        {pieces.map((p: PieceRow) => (
          <li key={p.id}>
            <Link to={pautaPath(p.id, p.status)} className="flex items-center justify-between gap-3 px-4 py-3 text-sm hover:bg-line/40">
              <span className="min-w-0">
                <span className="block truncate font-medium">
                  {p.title || s.piece.replace("{n}", String(p.number))}
                </span>
                <span className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted">
                  {p.created_at && <span>{fmtDate(p.created_at, lang)}</span>}
                  {(p.exported_formats ?? []).map((f) => (
                    <span key={f} className="rounded-full bg-teal-soft px-1.5 py-0.5 text-[10px] font-medium text-teal">
                      {FORMAT_LABEL[f] ?? f}
                    </span>
                  ))}
                </span>
              </span>
              <Badge tone={p.status === "draft" ? "warn" : "teal"}>{s.status[p.status]}</Badge>
            </Link>
          </li>
        ))}
      </ul>

      {/* fixed just above the global bottom nav on phone — at bottom-0
          the z-40 nav covered it and taps hit Configurações (KLOX-180) */}
      {picked.length > 0 && (
        <div
          className="fixed inset-x-0 bottom-[calc(3.5rem+env(safe-area-inset-bottom))] z-20 border-t border-line bg-paper-raised/95 px-4 py-3 backdrop-blur md:bottom-0"
          role="region"
          aria-label={s.build}
        >
          <div className="mx-auto flex max-w-3xl items-center justify-between gap-3">
            <p className="text-sm font-medium" aria-live="polite">
              {picked.length === 1 ? s.selected1 : s.selected.replace("{n}", String(picked.length))}
            </p>
            <Button disabled={build.isPending} onClick={() => build.mutate()}>
              {build.isPending ? s.building : s.build}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
