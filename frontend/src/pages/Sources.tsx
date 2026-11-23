import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api, type PreviewItem } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Badge, Button, Card, SkeletonLine } from "../ui";
import { StepCounter } from "../components/StepCounter";

// /publications/:id/sources — the onboarding sources screen: suggested fixture
// sources with real previews, add/remove, and confirm → auto first run.
// Its headline job is the aderência controls (KLOX-94): the theme, reject
// terms and per-section pautas the suggestion engine ranks by.
export function SourcesPage() {
  const { id } = useParams();
  const { t } = useT();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [previews, setPreviews] = useState<Record<string, PreviewItem[] | "loading">>({});
  const [previewNote, setPreviewNote] = useState<Record<string, string>>({});
  const [sel, setSel] = useState<Record<string, boolean>>({});
  const [adForm, setAdForm] = useState<{ topic: string; rejects: string; sections: Record<string, string> } | null>(null);
  const [adSaved, setAdSaved] = useState(false);

  const pub = useQuery({ queryKey: ["publication", id], queryFn: () => api.publication(id!) });
  const sources = useQuery({ queryKey: ["sources", id], queryFn: () => api.sources(id!) });
  const catalog = useQuery({ queryKey: ["catalog", id], queryFn: () => api.sourceCatalog(id!) });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["sources", id] });
    qc.invalidateQueries({ queryKey: ["catalog", id] });
  };

  useEffect(() => {
    if (pub.data && !adForm) {
      const p = pub.data.publication;
      setAdForm({
        topic: p.topic,
        rejects: p.reject_terms ?? "",
        sections: Object.fromEntries(p.sections.map((s) => [s.id, s.topic ?? ""])),
      });
    }
  }, [pub.data, adForm]);

  // the aderência save sends the full section list back with the new
  // topics — the PUT validates and replaces it wholesale (KLOX-94)
  const saveAd = useMutation({
    mutationFn: () =>
      api.updatePublication(id!, {
        topic: adForm!.topic,
        reject_terms: adForm!.rejects,
        sections: pub.data!.publication.sections.map((s) => ({
          ...s,
          topic: adForm!.sections[s.id] ?? "",
        })),
      }),
    onSuccess: () => {
      setAdSaved(true);
      qc.invalidateQueries({ queryKey: ["publication", id] });
    },
  });
  const editAd = (patch: Partial<NonNullable<typeof adForm>>) => {
    setAdForm((f) => (f ? { ...f, ...patch } : f));
    setAdSaved(false);
  };

  const remove = useMutation({
    mutationFn: (sid: number) => api.sourceDelete(id!, sid),
    onSuccess: refresh,
  });

  // KLOX-163: the full desk list — every editoria in the catalog
  // taxonomy with its pick state, auditable, with add/remove. A
  // removal also unpicks the desk's sources, so it lands on the same
  // queries as a single-source change.
  const desks = useQuery({ queryKey: ["editorias", id], queryFn: () => api.editorias(id!) });
  const deskMut = useMutation({
    mutationFn: (v: { key: string; on: boolean }) =>
      v.on ? api.editoriaRemove(id!, v.key) : api.editoriaAdd(id!, v.key),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["editorias", id] });
      qc.invalidateQueries({ queryKey: ["publication", id] });
      refresh();
    },
  });
  const checked = (u: string) => sel[u] !== false;
  // the catalog only lists sources not yet added — the belt-and-
  // suspenders filter also drops an offered row the moment a source
  // with that URL exists
  const addedURLs = new Set((sources.data ?? []).map((s) => s.url));
  const offered = (catalog.data?.offered ?? []).filter((o) => !addedURLs.has(o.url));
  const count = (sources.data?.length ?? 0) + offered.filter((o) => checked(o.url)).length;
  // while the catalog is in flight the lists are unknown — render a
  // loading line instead of flashing an empty state + "no sources"
  // hint that the arriving data contradicts a moment later (KLOX-56)
  const loading = sources.isLoading || catalog.isLoading;
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
      {!pub.data?.latest_issue && <StepCounter steps={t.steps.onboarding} current={3} />}
      <h1 className="mt-4 font-display text-2xl font-bold">
        {t.sourcesPage.title}
        {pub.data && <span className="ml-2 text-base font-normal text-muted">— {pub.data.publication.name}</span>}
      </h1>
      <p className="mt-1 text-sm text-muted">{t.sourcesPage.subtitle}</p>

      {/* aderência controls (KLOX-94): this page's job — the topics the
          suggestion engine ranks by, in the creator's own words. Custom-
          source submission is off the table by owner decision: the page
          manages the catalog feeds plus these controls, nothing else. */}
      {pub.data && adForm && (
        <Card className="mt-4 p-4">
          <h2 className="text-xs font-semibold uppercase tracking-wide text-muted">{t.sourcesPage.aderenciaTitle}</h2>
          <p className="mt-1 text-xs text-muted">{t.sourcesPage.aderenciaHint}</p>
          <div className="mt-3 space-y-3">
            <label className="block">
              <span className="text-xs font-medium text-muted">{t.sourcesPage.newsletterTheme}</span>
              <input
                value={adForm.topic}
                onChange={(e) => editAd({ topic: e.target.value })}
                placeholder={t.sourcesPage.themePlaceholder}
                className="mt-1 w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
              />
            </label>
            <label className="block">
              <span className="text-xs font-medium text-muted">{t.sourcesPage.rejectsLabel}</span>
              <input
                value={adForm.rejects}
                onChange={(e) => editAd({ rejects: e.target.value })}
                placeholder={t.create.rejectTerms}
                className="mt-1 w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
              />
              <span className="mt-1 block text-xs text-muted">{t.create.rejectTermsHint}</span>
            </label>
            {pub.data.publication.sections.map((s) => (
              <label key={s.id} className="block">
                <span className="text-xs font-medium text-muted">
                  {s.title || "—"} · {t.sourcesPage.sectionTopicLabel}
                </span>
                <input
                  value={adForm.sections[s.id] ?? ""}
                  onChange={(e) => editAd({ sections: { ...adForm.sections, [s.id]: e.target.value } })}
                  placeholder={t.sourcesPage.sectionTopicEmpty}
                  className="mt-1 w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
                />
              </label>
            ))}
          </div>
          <div className="mt-3 flex items-center gap-3">
            {/* outline: the screen's one primary is the forward action
                below — two filled buttons competed for it (§4.3, KLOX-68) */}
            <Button variant="outline" size="sm" disabled={saveAd.isPending} onClick={() => saveAd.mutate()}>
              {t.sourcesPage.save}
            </Button>
            {adSaved && <span className="text-xs text-teal">{t.sourcesPage.saved}</span>}
          </div>
          {saveAd.error && <p className="mt-2 text-xs text-danger">{errText(t, saveAd.error)}</p>}
        </Card>
      )}

      {/* KLOX-163: every desk in the taxonomy — the auditable list the
          owner asked for, not only the sources this cycle happened to
          show. Picked sources render darker so the desk's real feed is
          visible at a glance. */}
      {desks.data && (
        <Card className="mt-4 p-4">
          <h2 className="text-xs font-semibold uppercase tracking-wide text-muted">{t.sourcesPage.editoriasTitle}</h2>
          <p className="mt-1 text-xs text-muted">{t.sourcesPage.editoriasHint}</p>
          <ul className="mt-2 divide-y divide-line">
            {desks.data.editorias.map((e) => {
              const on = e.active || e.selected;
              return (
                <li key={e.key} className="flex items-start gap-3 py-2.5">
                  <div className="min-w-0 flex-1">
                    <p className="flex items-center gap-2 text-sm font-medium">
                      {t.sourcesPage.editoriaLabels[e.key] ?? e.label}
                      {e.active && <Badge tone="teal">{t.sourcesPage.editoriaActive}</Badge>}
                      {!e.active && e.selected && <Badge>{t.sourcesPage.editoriaSelected}</Badge>}
                    </p>
                    <p className="mt-0.5 text-xs text-muted">
                      {e.sources.length === 0
                        ? t.sourcesPage.editoriaEmpty
                        : e.sources.map((s, i) => (
                            <span key={s.slug} className={s.picked ? "font-medium text-ink" : undefined}>
                              {i > 0 && " · "}
                              {s.name}
                              {s.suspended ? ` (${t.sourcesPage.suspendedBadge})` : ""}
                            </span>
                          ))}
                    </p>
                  </div>
                  <Button
                    variant="outline" size="sm" disabled={deskMut.isPending}
                    onClick={() => deskMut.mutate({ key: e.key, on })}
                  >
                    {on ? t.sourcesPage.editoriaRemove : t.sourcesPage.editoriaAdd}
                  </Button>
                </li>
              );
            })}
          </ul>
          {desks.data.custom.length > 0 && (
            <>
              <h3 className="mt-4 text-xs font-semibold uppercase tracking-wide text-muted">
                {t.sourcesPage.editoriaCustomTitle}
              </h3>
              <ul className="mt-1 text-xs text-muted">
                {desks.data.custom.map((s) => (
                  <li key={s.source_id} className="truncate py-0.5">{s.name || s.url}</li>
                ))}
              </ul>
            </>
          )}
        </Card>
      )}
      {deskMut.error && <p className="mt-2 text-xs text-danger">{errText(t, deskMut.error)}</p>}

      {loading && <p className="mt-6 text-sm text-muted">{t.sourcesPage.loading}</p>}

      {!loading && (sources.data?.length ?? 0) > 0 && (
        <>
          <h2 className="mt-6 text-xs font-semibold uppercase tracking-wide text-muted">{t.sourcesPage.added}</h2>
          <div className="mt-2 grid grid-cols-1 gap-2">
            {sources.data!.map((s) => (
              <Card key={s.id} className="flex items-center gap-3 p-3">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{s.name || s.url}</p>
                  <p className="truncate text-xs text-muted">{s.url}</p>
                  {s.last_status && (
                    <p className="mt-0.5 text-xs text-muted">
                      <Badge tone={s.last_status.startsWith("ok") ? "teal" : "warn"}>
                        {s.last_status.startsWith("ok") ? t.sourcesPage.lastOk : t.sourcesPage.lastFail}
                      </Badge>
                    </p>
                  )}
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

      {!loading && offered.length > 0 && (
        <>
          <h2 className="mt-6 text-xs font-semibold uppercase tracking-wide text-muted">{t.sourcesPage.offered}</h2>
          <div className="mt-2 grid grid-cols-1 gap-2">
            {offered.map((o) => (
              <Card key={o.url} className="p-3">
                <div className="flex items-center gap-3">
                  <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-3">
                    <input
                      type="checkbox"
                      className="h-4 w-4 accent-teal"
                      aria-label={o.name || o.url}
                      checked={checked(o.url)}
                      onChange={(e) => setSel((s) => ({ ...s, [o.url]: e.target.checked }))}
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">{o.name || o.url}</span>
                      <span className="mt-0.5 flex items-center gap-2 text-xs text-muted">
                        <Badge>{o.type === "rss" ? t.sourcesPage.ownType.rss : t.sourcesPage.ownType.page}</Badge>
                        {o.suspended && <Badge tone="warn">{t.sourcesPage.suspendedBadge}</Badge>}
                      </span>
                      {o.note && <span className="mt-0.5 block text-xs text-muted">{o.note}</span>}
                    </span>
                  </label>
                  <Button variant="outline" size="sm" onClick={() => preview(o.type, o.url)}>
                    {t.sourcesPage.preview}
                  </Button>
                </div>
                {previews[o.url] === "loading" && <SkeletonLine className="mt-2 h-3 w-32" />}
                {Array.isArray(previews[o.url]) && (
                  <ul className="mt-2 grid grid-cols-1 gap-1 border-t border-line pt-2 text-sm">
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

      {/* one button pattern across the onboarding steps (KLOX-79):
          Fontes gets the same pair as Seções — Voltar left, Avançar
          right; the forward action keeps the same right-aligned spot.
          Voltar returns to the publication's sections step. Once a
          first issue exists this is the sources screen again and keeps
          its original confirm. */}
      <div className="mt-6 flex items-center gap-3">
        {!pub.data?.latest_issue ? (
          <>
            <span className="text-xs text-muted">
              {count === 0 && !loading ? t.sourcesPage.empty : count > 0 ? t.sourcesPage.selected(count) : null}
            </span>
            <div className="ml-auto flex items-center gap-3">
              <Button
                variant="outline"
                disabled={confirm.isPending}
                onClick={() => nav(`/publications/${id}/sections?onboarding=1`)}
              >
                {t.nav.back}
              </Button>
              <Button disabled={count === 0 || confirm.isPending} onClick={() => confirm.mutate()}>
                {confirm.isPending ? t.sourcesPage.confirming : t.create.next}
              </Button>
            </div>
          </>
        ) : (
          <div className="ml-auto flex items-center gap-3">
            <Button
              disabled={count === 0 || confirm.isPending}
              onClick={() => confirm.mutate()}
              size="lg"
            >
              {confirm.isPending ? t.sourcesPage.confirming : t.sourcesPage.confirm(count)}
            </Button>
            {count === 0 && !loading && <span className="text-xs text-muted">{t.sourcesPage.empty}</span>}
          </div>
        )}
      </div>
      {confirm.error && <p className="mt-2 text-xs text-danger">{errText(t, confirm.error)}</p>}
    </div>
  );
}
