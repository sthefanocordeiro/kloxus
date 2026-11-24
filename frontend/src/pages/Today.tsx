import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiRequestError, type PautaCard, type PieceRow } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Badge, Button, Card, SkeletonLine } from "../ui";
import { useToday } from "./todayStrings";

// /publications/:id/today — Hoje (KLOX-123 slice 2): the day's pauta for
// the radar with Sim/Não on every card (training continues) and
// "Escrever", which turns the story into a Peça or adds it to the open
// one; below it, the pieces in progress. No model is called here.
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

  const answer = useMutation({
    mutationFn: (v: { c: PautaCard; a: "yes" | "no" }) => api.calibrationAnswer(id!, v.c.story_id, v.a),
    onSuccess: (_r, v) => {
      if (v.a === "no") setGone((p) => new Set(p).add(v.c.story_id));
      else setLiked((p) => new Set(p).add(v.c.story_id));
    },
  });

  const write = useMutation({
    mutationFn: async (c: PautaCard) => {
      const res = await api.todayWrite(id!, c.story_id);
      if (res.add_block) {
        // writing already began: the editor's block insert places the pauta
        const doc = await api.document(res.issue_id);
        await api.blockInsert(res.issue_id, {
          section: res.section ?? "", kind: "item", story_id: c.story_id, version: doc.version,
        });
      }
      return res.issue_id;
    },
    onSuccess: (issueID) => {
      qc.invalidateQueries({ queryKey: key });
      nav(`/issues/${issueID}`);
    },
    onError: (err) => {
      // the deck was rebuilt since these cards were served
      if (err instanceof ApiRequestError && err.status === 404) qc.invalidateQueries({ queryKey: key });
    },
  });

  const cards = (q.data?.cards ?? []).filter((c) => !gone.has(c.story_id));
  const pieces = q.data?.pieces ?? [];
  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-6">
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
      {write.isError && !(write.error instanceof ApiRequestError && write.error.status === 404) && (
        <p className="mt-4 text-sm text-danger" role="alert">{errText(t, write.error)}</p>
      )}

      {q.data && cards.length === 0 && (
        <Card className="mt-6 p-5">
          <p className="text-sm text-muted">{s.empty}</p>
        </Card>
      )}

      <ul className="mt-6 space-y-3" aria-label={s.title}>
        {cards.map((c) => (
          <li key={c.story_id}>
            <Card className="p-4" data-testid="today-card">
              <h2 className="font-display text-lg font-semibold leading-snug">{c.title}</h2>
              <p className="mt-1 text-xs text-muted">
                {c.outlet}
                {c.outlets > 1 && ` · ${s.outlets.replace("{n}", String(c.outlets))}`}
                {c.age_hours !== null && ` · ${s.hoursAgo.replace("{n}", String(Math.round(c.age_hours)))}`}
              </p>
              {c.summary && <p className="mt-2 text-sm">{c.summary}</p>}
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <Button size="sm" disabled={write.isPending} onClick={() => write.mutate(c)}>
                  {s.write}
                </Button>
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
        ))}
      </ul>

      <h2 className="mt-10 font-display text-xl font-bold">{s.piecesTitle}</h2>
      {q.data && pieces.length === 0 && <p className="mt-2 text-sm text-muted">{s.noPieces}</p>}
      <ul className="mt-3 divide-y divide-line rounded border border-line bg-paper-raised">
        {pieces.map((p: PieceRow) => (
          <li key={p.id}>
            <Link to={`/issues/${p.id}`} className="flex items-center justify-between px-4 py-3 text-sm hover:bg-line/40">
              <span className="font-medium">{s.piece.replace("{n}", String(p.number))}</span>
              <Badge tone={p.status === "draft" ? "warn" : "teal"}>{s.status[p.status]}</Badge>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}
