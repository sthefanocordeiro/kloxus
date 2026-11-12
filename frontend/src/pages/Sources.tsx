import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type PreviewItem } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Badge, Button, Card } from "../ui";

// /publications/:id/sources — the onboarding sources screen: suggested fixture
// sources with real previews, add/remove, and confirm → auto first run.
export function SourcesPage() {
  const { id } = useParams();
  const { t } = useT();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [previews, setPreviews] = useState<Record<string, PreviewItem[] | "loading">>({});
  const [previewNote, setPreviewNote] = useState<Record<string, string>>({});
  const [sel, setSel] = useState<Record<string, boolean>>({});
  const [ownURL, setOwnURL] = useState("");
  const [ownName, setOwnName] = useState("");
  const [ownType, setOwnType] = useState<"rss" | "page">("rss");
  const [ownPreview, setOwnPreview] = useState<{ items: PreviewItem[]; note?: string } | null>(null);

  const pub = useQuery({ queryKey: ["publication", id], queryFn: () => api.publication(id!) });
  const sources = useQuery({ queryKey: ["sources", id], queryFn: () => api.sources(id!) });
  const catalog = useQuery({ queryKey: ["catalog", id], queryFn: () => api.sourceCatalog(id!) });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["sources", id] });
    qc.invalidateQueries({ queryKey: ["catalog", id] });
  };

  const add = useMutation({
    mutationFn: ({ type, url, name }: { type: string; url: string; name?: string }) =>
      api.sourceAdd(id!, type, url, name),
    onSuccess: () => { refresh(); setOwnURL(""); setOwnName(""); setOwnPreview(null); },
  });
  const remove = useMutation({
    mutationFn: (sid: number) => api.sourceDelete(id!, sid),
    onSuccess: refresh,
  });
  const checked = (u: string) => sel[u] !== false;
  // the catalog only lists sources not yet added — the belt-and-
  // suspenders filter also drops an offered row the moment a source
  // with that URL exists
  const addedURLs = new Set((sources.data ?? []).map((s) => s.url));
  const offered = (catalog.data ?? []).filter((o) => !addedURLs.has(o.url));
  const count = (sources.data?.length ?? 0) + offered.filter((o) => checked(o.url)).length;
  const confirm = useMutation({
    mutationFn: async () => {
      await api.sourceConfirm(id!, offered.filter((o) => checked(o.url)).map((o) => o.url));
      return api.startRun(Number(id));
    },
    onSuccess: (run) => nav(`/runs/${run.id}`, { replace: true }),
  });

  const preview = async (type: string, url: string) => {
    setPreviews((p) => ({ ...p, [url]: "loading" }));
    try {
      const r = await api.sourcePreview(id!, type, url);
      setPreviews((p) => ({ ...p, [url]: r.items }));
      if (r.note) setPreviewNote((n) => ({ ...n, [url]: r.note! }));
    } catch {
      setPreviews((p) => ({ ...p, [url]: [] }));
    }
  };

  return (
    <div className="mx-auto w-full max-w-2xl px-4 py-8">
      <Link to={`/publications/${id}`} className="text-xs text-muted hover:text-ink">←</Link>
      <h1 className="mt-1 font-doc text-2xl font-semibold">
        {t.sourcesPage.title}
        {pub.data && <span className="ml-2 text-base font-normal text-muted">— {pub.data.publication.name}</span>}
      </h1>
      <p className="mt-1 text-sm text-muted">{t.sourcesPage.subtitle}</p>

      {(sources.data?.length ?? 0) > 0 && (
        <>
          <h2 className="mt-6 text-xs font-semibold uppercase tracking-wide text-muted">{t.sourcesPage.added}</h2>
          <div className="mt-2 grid gap-2">
            {sources.data!.map((s) => (
              <Card key={s.id} className="flex items-center gap-3 p-3">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{s.name || s.url}</p>
                  <p className="truncate text-xs text-muted">{s.url}</p>
                  <p className="mt-0.5 text-xs text-muted">
                    {t.sourcesPage.itemsWeek(s.items_per_week)}
                    {s.last_status && (
                      <Badge className="ml-2" tone={s.last_status === "ok" ? "teal" : "warn"}>
                        {s.last_status === "ok" ? t.sourcesPage.lastOk : t.sourcesPage.lastFail}
                      </Badge>
                    )}
                  </p>
                </div>
                <Button
                  variant="outline" size="sm" disabled={remove.isPending}
                  onClick={() => remove.mutate(s.id)}
                >
                  {t.sourcesPage.remove}
                </Button>
              </Card>
            ))}
          </div>
        </>
      )}

      {offered.length > 0 && (
        <>
          <h2 className="mt-6 text-xs font-semibold uppercase tracking-wide text-muted">{t.sourcesPage.offered}</h2>
          <div className="mt-2 grid gap-2">
            {offered.map((o) => (
              <Card key={o.url} className="p-3">
                <div className="flex items-center gap-3">
                  <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-3">
                    <input
                      type="checkbox"
                      className="h-4 w-4 accent-teal"
                      checked={checked(o.url)}
                      onChange={(e) => setSel((s) => ({ ...s, [o.url]: e.target.checked }))}
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">{o.name || o.url}</span>
                      <span className="mt-0.5 flex items-center gap-2 text-xs text-muted">
                        <Badge>{o.type === "rss" ? t.sourcesPage.ownType.rss : t.sourcesPage.ownType.page}</Badge>
                        {o.items_per_week != null && o.items_per_week > 0 && (
                          <span>{t.sourcesPage.itemsWeek(o.items_per_week)}</span>
                        )}
                      </span>
                    </span>
                  </label>
                  <Button variant="outline" size="sm" onClick={() => preview(o.type, o.url)}>
                    {t.sourcesPage.preview}
                  </Button>
                </div>
                {previews[o.url] === "loading" && <p className="mt-2 text-xs text-muted">…</p>}
                {Array.isArray(previews[o.url]) && (
                  <ul className="mt-2 grid gap-1 border-t border-line pt-2 text-sm">
                    {(previews[o.url] as PreviewItem[]).map((it, i) => (
                      <li key={i} className="truncate">
                        <a href={it.url} target="_blank" rel="noreferrer" className="hover:text-teal">
                          {it.title}
                        </a>
                        <span className="ml-1 text-xs text-muted">— {it.outlet}</span>
                      </li>
                    ))}
                    {previewNote[o.url] && <li className="text-xs text-muted">{previewNote[o.url]}</li>}
                  </ul>
                )}
              </Card>
            ))}
          </div>
        </>
      )}

      <Card className="mt-6 p-4">
        <h2 className="text-xs font-semibold uppercase tracking-wide text-muted">{t.sourcesPage.own}</h2>
        <div className="mt-2 flex flex-wrap gap-2">
          <select
            value={ownType}
            onChange={(e) => setOwnType(e.target.value as "rss" | "page")}
            className="rounded border border-line bg-paper px-2 py-2 text-sm"
          >
            <option value="rss">{t.sourcesPage.ownType.rss}</option>
            <option value="page">{t.sourcesPage.ownType.page}</option>
          </select>
          <input
            value={ownName}
            onChange={(e) => setOwnName(e.target.value)}
            placeholder={t.sourcesPage.ownName}
            aria-label={t.sourcesPage.ownName}
            className="w-36 rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
          />
          <input
            value={ownURL}
            onChange={(e) => { setOwnURL(e.target.value); setOwnPreview(null); }}
            placeholder={t.sourcesPage.ownPlaceholder}
            className="min-w-0 flex-1 rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
          />
          <Button
            variant="outline" size="sm" disabled={!ownURL.trim()}
            onClick={async () => setOwnPreview(await api.sourcePreview(id!, ownType, ownURL).catch(() => ({ items: [] })))}
          >
            {t.sourcesPage.preview}
          </Button>
          <Button
            variant="outline" size="sm" disabled={!ownURL.trim() || add.isPending}
            onClick={() => add.mutate({ type: ownType, url: ownURL.trim(), name: ownName.trim() || undefined })}
          >
            {t.sourcesPage.ownAdd}
          </Button>
        </div>
        {ownPreview?.note && <p className="mt-2 text-xs text-muted">{t.sourcesPage.simulated}</p>}
        {ownPreview && ownPreview.items.length === 0 && !ownPreview.note && (
          <p className="mt-2 text-xs text-muted">{t.sourcesPage.simulated}</p>
        )}
        {add.error && <p className="mt-2 text-xs text-danger">{errText(t, add.error)}</p>}
      </Card>

      <div className="mt-6 flex items-center gap-3">
        <Button
          disabled={count === 0 || confirm.isPending}
          onClick={() => confirm.mutate()}
          size="lg"
        >
          {confirm.isPending ? t.sourcesPage.confirming : t.sourcesPage.confirm(count)}
        </Button>
        {count === 0 && <span className="text-xs text-muted">{t.sourcesPage.empty}</span>}
      </div>
      {confirm.error && <p className="mt-2 text-xs text-danger">{errText(t, confirm.error)}</p>}
    </div>
  );
}
