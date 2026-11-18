import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, type Proposal } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Badge, Button, Card } from "../ui";
import { StepCounter } from "../components/StepCounter";
import { SectionBuilder, totalStories, SECTION_LIMITS } from "../components/SectionBuilder";

// /new — the create-newsletter flow in two steps (§3.1): Sua newsletter
// (paste card + the five editable blocks) → Seções (the section
// builder) → Fontes. Creating the publication is a plain POST — no AI
// call happens anywhere in onboarding.
export function CreateNewsletter() {
  const { t } = useT();
  const nav = useNavigate();
  const proposal = useQuery({ queryKey: ["proposal"], queryFn: api.proposal });
  const [form, setForm] = useState<Proposal | null>(null);
  const [step, setStep] = useState(0); // 0 = newsletter, 1 = sections
  const [material, setMaterial] = useState("");
  const [liveOnly, setLiveOnly] = useState(false);

  useEffect(() => {
    if (proposal.data && !form) setForm(proposal.data);
  }, [proposal.data, form]);

  const propose = useMutation({
    mutationFn: () => api.propose(material),
    onError: () => setLiveOnly(true),
  });

  const create = useMutation({
    mutationFn: () => {
      const { sources: _s, ...body } = form!;
      return api.createPublication(body);
    },
    onSuccess: (pub) => nav(`/publications/${pub.id}/sources`, { replace: true }),
  });

  const set = (k: keyof Proposal, v: string) => form && setForm({ ...form, [k]: v });
  const sectionsOK =
    !!form && form.sections.length > 0 && totalStories(form.sections) <= SECTION_LIMITS.totalMax &&
    form.sections.every((s) => s.title.trim() !== "");

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-8">
      <StepCounter steps={t.steps.onboarding} current={step + 1} onStep={(i) => i < 2 && setStep(i)} />
      <h1 className="mt-4 font-display text-2xl font-bold">
        {step === 0 ? t.create.title : t.create.sectionsTitle}
      </h1>
      {step === 1 && <p className="mt-1 text-sm text-muted">{t.create.sectionsHint}</p>}

      {step === 0 && (
        <>
          <Card className="mt-6 p-5">
            <label className="text-xs font-semibold uppercase tracking-wide text-muted" htmlFor="paste">
              {t.create.pasteLabel}
            </label>
            <div className="mt-2 flex gap-2">
              <input
                id="paste"
                value={material}
                onChange={(e) => { setMaterial(e.target.value); setLiveOnly(false); }}
                placeholder={t.create.pastePlaceholder}
                className="min-w-0 flex-1 rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
              />
              <Button
                variant="outline"
                size="sm"
                disabled={!material.trim() || propose.isPending}
                onClick={() => propose.mutate()}
              >
                {t.create.pasteAction}
              </Button>
            </div>
            {liveOnly && <p className="mt-2 text-xs text-muted">{t.create.pasteLiveOnly}</p>}
          </Card>

          {form && (
            <Card className="mt-6 p-5">
              <div className="mb-4 flex items-center gap-2">
                <Badge tone="teal">{t.create.sampleBadge}</Badge>
                <span className="text-xs text-muted">{t.create.sampleHint}</span>
              </div>

              <Field label={t.create.name} value={form.name} onChange={(v) => set("name", v)} />
              <Field label={t.create.topic} value={form.topic} onChange={(v) => set("topic", v)} />
              <Field label={t.create.audience} value={form.audience} onChange={(v) => set("audience", v)} />
              <Field label={t.create.editorialLine} value={form.editorial_line} onChange={(v) => set("editorial_line", v)} area />
              <Field label={t.create.tone} value={form.tone} onChange={(v) => set("tone", v)} />

              <div className="mt-5 flex items-center gap-3">
                <Button disabled={!form.name.trim()} onClick={() => setStep(1)}>
                  {t.create.nextSections}
                </Button>
              </div>
            </Card>
          )}
        </>
      )}

      {step === 1 && form && (
        <Card className="mt-6 p-5">
          <SectionBuilder value={form.sections} onChange={(sections) => setForm({ ...form, sections })} />
          {create.error && <p className="mt-3 text-xs text-danger">{errText(t, create.error)}</p>}
          <div className="mt-5 flex items-center gap-3">
            <Button disabled={create.isPending || !sectionsOK} onClick={() => create.mutate()}>
              {create.isPending ? t.create.creating : t.create.action}
            </Button>
          </div>
        </Card>
      )}
    </div>
  );
}

function Field({ label, value, onChange, area }: {
  label: string; value: string; onChange: (v: string) => void; area?: boolean;
}) {
  return (
    <label className="mt-3 block">
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
          onChange={(e) => onChange(e.target.value)}
          className="mt-1 w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
        />
      )}
    </label>
  );
}
