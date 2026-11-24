import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api, type Publication } from "../api";
import { useT } from "../i18n";
import { usePublishing } from "../lib/publishing";
import { errText } from "../lib/errors";
import { Button, Card } from "../ui";
import { KDialog } from "../ui-overlays";

// Configurações (§3.1, KLOX-65): the publication's editorial identity,
// editable — plus the danger zone that deletes the newsletter behind an
// explicit confirmation.
export function SettingsPage() {
  const { t } = useT();
  const publishing = usePublishing();
  const { id } = useParams();
  const nav = useNavigate();
  const qc = useQueryClient();
  const pub = useQuery({ queryKey: ["publication", id], queryFn: () => api.publication(id!) });
  const [form, setForm] = useState<Publication | null>(null);
  const [saved, setSaved] = useState(false);
  const [confirming, setConfirming] = useState(false);

  useEffect(() => {
    if (pub.data && !form) setForm(pub.data.publication);
  }, [pub.data, form]);

  const save = useMutation({
    mutationFn: () =>
      api.updatePublication(id!, {
        name: form!.name,
        topic: form!.topic,
        reject_terms: form!.reject_terms,
        paywall: form!.paywall,
      }),
    onSuccess: () => {
      setSaved(true);
      qc.invalidateQueries({ queryKey: ["publication", id] });
      qc.invalidateQueries({ queryKey: ["publications"] });
    },
  });

  const del = useMutation({
    mutationFn: () => api.deletePublication(id!),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["publications"] });
      nav("/publications", { replace: true });
    },
  });

  const p = pub.data?.publication;
  if (!p || !form) return <div className="p-6 text-sm text-muted">…</div>;

  const set = (k: keyof Publication, v: string) => {
    setForm({ ...form, [k]: v });
    setSaved(false);
  };

  return (
    <div className="mx-auto w-full max-w-3xl space-y-4 p-4 md:p-6">
      <Card className="p-5">
        <Field label={t.settingsPage.name} value={form.name} onChange={(v) => set("name", v)} />
        <Field label={t.settingsPage.topic} value={form.topic} onChange={(v) => set("topic", v)} />
        <Field label={t.settingsPage.rejectTerms} value={form.reject_terms ?? ""} onChange={(v) => set("reject_terms", v)} hint={t.settingsPage.rejectTermsHint} />
        {publishing && form.paywall.enabled && (
          <Field
            label={t.settingsPage.price}
            value={form.paywall.price}
            placeholder={t.settingsPage.pricePlaceholder}
            onChange={(v) => {
              setForm({ ...form, paywall: { ...form.paywall, price: v } });
              setSaved(false);
            }}
          />
        )}
        {save.error && <p className="mt-3 text-xs text-danger">{errText(t, save.error)}</p>}
        {saved && <p className="mt-3 text-xs font-medium text-teal">{t.settingsPage.saved}</p>}
        <div className="mt-5">
          <Button disabled={save.isPending || !form.name.trim()} onClick={() => save.mutate()}>
            {save.isPending ? t.settingsPage.saving : t.settingsPage.save}
          </Button>
        </div>
      </Card>

      <Card className="border-danger/40 p-5">
        <h2 className="font-display text-base font-bold text-danger">{t.settingsPage.dangerTitle}</h2>
        <p className="mt-1 text-sm text-muted">{t.settingsPage.dangerBody}</p>
        {del.error && <p className="mt-2 text-xs text-danger">{errText(t, del.error)}</p>}
        <div className="mt-4">
          <Button variant="dangerGhost" onClick={() => setConfirming(true)}>
            {t.settingsPage.deleteAction}
          </Button>
        </div>
      </Card>

      <KDialog open={confirming} onOpenChange={setConfirming} title={t.settingsPage.deleteConfirmTitle}>
        <p className="text-sm text-muted">{t.settingsPage.deleteConfirmBody(p.name)}</p>
        <div className="mt-5 flex gap-3">
          <Button
            variant="danger"
            disabled={del.isPending}
            onClick={() => del.mutate()}
          >
            {del.isPending ? t.settingsPage.deleting : t.settingsPage.deleteConfirmAction}
          </Button>
          <Button variant="outline" onClick={() => setConfirming(false)}>
            {t.settingsPage.cancel}
          </Button>
        </div>
      </KDialog>
    </div>
  );
}

function Field({ label, value, onChange, area, placeholder, hint }: {
  label: string; value: string; onChange: (v: string) => void; area?: boolean; placeholder?: string; hint?: string;
}) {
  return (
    <label className="mt-3 block first:mt-0">
      <span className="text-xs font-semibold uppercase tracking-wide text-muted">{label}</span>
      {area ? (
        <textarea
          value={value}
          onChange={(e) => onChange(e.target.value)}
          rows={3}
          className="mt-1 w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
        />
      ) : (
        <input
          value={value}
          placeholder={placeholder}
          onChange={(e) => onChange(e.target.value)}
          className="mt-1 w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
        />
      )}
      {hint && <span className="mt-1 block text-xs text-muted">{hint}</span>}
    </label>
  );
}
