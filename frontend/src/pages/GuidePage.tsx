import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useParams } from "react-router-dom";
import { api, ApiRequestError } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Markdown } from "../markdown";
import { Badge, Button, Card } from "../ui";
import { NotFound } from "./ErrorPage";

// /publications/:id/guide — the editorial guide's screen: the rules the
// product learned from the creator's rejections and swaps, editable by
// hand and rebuildable from the decision history.
export function GuidePage() {
  const { id = "" } = useParams();
  const { t } = useT();
  const qc = useQueryClient();
  const guide = useQuery({ queryKey: ["guide", id], queryFn: () => api.guide(id) });
  const [draft, setDraft] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: (content: string) => api.guideSave(id, content),
    onSuccess: () => {
      setDraft(null);
      qc.invalidateQueries({ queryKey: ["guide", id] });
    },
  });
  const rebuild = useMutation({
    mutationFn: () => api.guideRebuild(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["guide", id] }),
  });

  if (guide.isLoading) {
    return <p className="p-6 text-sm text-muted">…</p>;
  }
  if (guide.isError) {
    if (guide.error instanceof ApiRequestError && guide.error.status === 404) {
      return <NotFound />;
    }
    return <p className="p-6 text-sm text-danger">{errText(t, guide.error)}</p>;
  }
  const g = guide.data!;

  return (
    <div className="mx-auto w-full max-w-2xl px-4 py-8">
      <div className="mt-1 flex items-baseline gap-3">
        <h1 className="font-display text-2xl font-bold">{t.guide.title}</h1>
        {g.version > 0 && <Badge>{t.guide.version(g.version)}</Badge>}
      </div>
      <p className="mt-1 text-sm text-muted">{t.guide.hint}</p>

      {draft !== null ? (
        <Card className="mt-4 p-4">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            rows={14}
            className="w-full rounded border border-line bg-paper px-3 py-2 font-mono text-sm outline-none focus:border-teal"
          />
          {save.error && <p className="mt-2 text-xs text-danger">{errText(t, save.error)}</p>}
          <div className="mt-3 flex gap-2">
            <Button size="sm" disabled={save.isPending} onClick={() => save.mutate(draft)}>
              {t.guide.save}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setDraft(null)}>
              {t.guide.cancel}
            </Button>
          </div>
        </Card>
      ) : g.content ? (
        <Card className="mt-4 p-4">
          <Markdown md={g.content} />
        </Card>
      ) : (
        <Card className="mt-4 p-4">
          <p className="text-sm text-muted">{t.guide.empty}</p>
        </Card>
      )}

      {draft === null && (
        <div className="mt-4 flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => setDraft(g.content)}>
            {t.guide.edit}
          </Button>
          <Button size="sm" variant="outline" disabled={rebuild.isPending} onClick={() => rebuild.mutate()}>
            {rebuild.isPending ? t.guide.rebuilding : t.guide.rebuild}
          </Button>
        </div>
      )}
      {rebuild.error && <p className="mt-2 text-xs text-danger">{errText(t, rebuild.error)}</p>}
      <p className="mt-2 text-xs text-muted">{t.guide.rebuildHint}</p>
    </div>
  );
}
