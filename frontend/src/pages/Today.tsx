import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiRequestError, type PautaCard, type PieceRow } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Badge, Button, Card, SkeletonLine } from "../ui";
import { useToday } from "./todayStrings";

// /publications/:id/today — Notícias de hoje (KLOX-168): the day's news
// for the radar with a checkbox on every card and Sim/Não (training
// continues). The ticking order becomes the text order; "Montar pauta →"
// opens the Escrever page. No model is called here.
export function Today() {
  const { id } = useParams();
  const { t } = useT();
  const s = useToday();
  const nav = useNavigate();
  const qc = useQueryClient();
  const key = ["today", id];
  const q = useQuery({ queryKey: key, queryFn: () => api.today(id!), staleTime: 60_000 });
  // answered cards: Não leaves the list, Sim stays marked
  const [gone, setGone] = useState<Set<number>>(new Set());
  const [liked, setLiked] = useState<Set<number>>(new Set());
  // story ids in the order they were ticked
  const [picked, setPicked] = useState<number[]>([]);

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
    onError: (err) => {
      // the deck was rebuilt since these cards were served
      if (err instanceof ApiRequestError && err.status === 404) qc.invalidateQueries({ queryKey: key });
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
      {q.isError && <p className="mt-6 text-sm text-danger" role="alert">{errText(t, q.error)}</p>}
      {build.isError && !(build.error instanceof ApiRequestError && build.error.status === 404) && (
        <p className="mt-4 text-sm text-danger" role="alert">{errText(t, build.error)}</p>
      )}

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
              <Card className="p-4" data-testid="today-card">
                <div className="flex items-start gap-3">
                  <input
                    type="checkbox"
                    className="mt-1.5 h-5 w-5 shrink-0 cursor-pointer accent-teal"
                    aria-label={`${s.select}: ${c.title}`}
                    checked={pos >= 0}
                    onChange={() => toggle(c.story_id)}
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
                <div className="mt-3 flex flex-wrap items-center gap-2 pl-8">
                  <Button
                    size="sm"
                    variant="outline"
                    aria-pressed={liked.has(c.story_id)}
                    disabled={answer.isPending || liked.has(c.story_id)}
                    onClick={() => answer.mutate({ c, a: "yes" })}
                  >
                    {liked.has(c.story_id) ? `✓ ${s.yes}` : s.yes}
                  </Button>
                  <Button size="sm" variant="outline" disabled={answer.isPending} onClick={() => answer.mutate({ c, a: "no" })}>
                    {s.no}
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
            <Link to={`/pauta/${p.id}`} className="flex items-center justify-between px-4 py-3 text-sm hover:bg-line/40">
              <span className="font-medium">{s.piece.replace("{n}", String(p.number))}</span>
              <Badge tone={p.status === "draft" ? "warn" : "teal"}>{s.status[p.status]}</Badge>
            </Link>
          </li>
        ))}
      </ul>

      {picked.length > 0 && (
        <div
          className="fixed inset-x-0 bottom-0 z-20 border-t border-line bg-paper-raised/95 px-4 py-3 backdrop-blur"
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
