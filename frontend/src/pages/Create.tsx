import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api, type Proposal } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Badge, Button, Card } from "../ui";

// /new — the create-newsletter screen. Paste-a-link is live-only in demo;
// the sample proposal fills five editable blocks the creator can tune
// before the publication is stored.
export function CreateNewsletter() {
  const { t } = useT();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const [email, setEmail] = useState("");
  const [sentTo, setSentTo] = useState("");
  const [demoLink, setDemoLink] = useState("");
  const owner = useQuery({
    queryKey: ["owner"],
    queryFn: api.owner,
    // keep checking while the magic link is open in another tab or inbox
    refetchInterval: (q) => (sentTo && !q.state.data?.confirmed ? 2500 : false),
  });
  const confirmed = owner.data?.confirmed ?? false;
  const linkInvalid = params.get("login") === "invalid";
  const sendLink = useMutation({
    mutationFn: () => api.sendMagicLink(email.trim()),
    onSuccess: (r) => {
      setSentTo(email.trim());
      setDemoLink(r.demo ? r.magic_link : "");
      if (linkInvalid) setParams({}, { replace: true });
    },
  });
  useEffect(() => {
    if (params.get("login") === "ok") qc.invalidateQueries({ queryKey: ["owner"] });
  }, [params, qc]);
  const proposal = useQuery({ queryKey: ["proposal"], queryFn: api.proposal });
  const [form, setForm] = useState<Proposal | null>(null);
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

  return (
    <div className="mx-auto w-full max-w-2xl px-4 py-8">
      <Link to="/" className="text-xs text-muted hover:text-ink">←</Link>
      <h1 className="mt-1 font-doc text-2xl font-semibold">{t.create.title}</h1>

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

      <Card className="mt-6 p-5" data-testid="email-gate">
        {!owner.data ? null : confirmed ? (
          <p className="text-sm text-teal">{t.create.confirmedAs(owner.data!.email)}</p>
        ) : (
          <>
            <h2 className="text-xs font-semibold uppercase tracking-wide text-muted">{t.create.gateTitle}</h2>
            <p className="mt-1 text-xs text-muted">{t.create.gateHint}</p>
            {linkInvalid && <p className="mt-2 text-xs text-danger">{t.create.linkInvalid}</p>}
            <form
              className="mt-3 flex gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                if (email.trim()) sendLink.mutate();
              }}
            >
              <input
                type="email"
                required
                aria-label={t.create.emailLabel}
                placeholder={t.create.emailLabel}
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="min-w-0 flex-1 rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
              />
              <Button type="submit" variant="outline" size="sm" disabled={!email.trim() || sendLink.isPending}>
                {sendLink.isPending ? t.create.sending : t.create.sendLink}
              </Button>
            </form>
            {sendLink.error && <p className="mt-2 text-xs text-danger">{errText(t, sendLink.error)}</p>}
            {sentTo && (
              <p className="mt-2 text-xs text-muted">
                {t.create.linkSent(sentTo)}
                {demoLink && (
                  <>
                    {" "}{t.create.demoInbox}{" "}
                    <a href={demoLink} className="text-teal underline underline-offset-2">{t.create.openLink}</a>
                  </>
                )}
              </p>
            )}
          </>
        )}
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

          <h3 className="mt-5 text-xs font-semibold uppercase tracking-wide text-muted">{t.create.sections}</h3>
          <ul className="mt-2 grid gap-1.5">
            {form.sections.map((s, i) => (
              <li key={i} className="flex items-baseline gap-2 text-sm">
                <input
                  value={s.title}
                  onChange={(e) =>
                    setForm({
                      ...form,
                      sections: form.sections.map((x, j) => (j === i ? { ...x, title: e.target.value } : x)),
                    })
                  }
                  className="min-w-0 flex-1 rounded border border-line bg-paper px-2 py-1 text-sm outline-none focus:border-teal"
                />
                <span className="shrink-0 text-xs text-muted">
                  {t.create.sectionItems(s.items)}
                  {s.commentary === "required" ? ` · ${t.create.commentaryReq}` : ""}
                </span>
              </li>
            ))}
          </ul>

          {create.error && <p className="mt-3 text-xs text-danger">{errText(t, create.error)}</p>}
          <div className="mt-5 flex items-center gap-3">
            <Button disabled={!confirmed || create.isPending || !form.name.trim()} onClick={() => create.mutate()}>
              {create.isPending ? t.create.creating : t.create.action}
            </Button>
            <span className="text-xs text-muted">{t.create.next}</span>
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
