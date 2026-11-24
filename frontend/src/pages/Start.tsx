import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { api, ApiRequestError, type PautaCard } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Button, Card, SkeletonLine } from "../ui";
import { StepCounter } from "../components/StepCounter";
import { ClaimEmail } from "../components/ClaimEmail";
import { NICHE_KEYS, useStart, type StartStrings } from "./startStrings";

type Phase = "about" | "calibrate" | "done";

// /start — onboarding in three steps (KLOX-123, KLOX-159 / D3-D4):
// Criar Radar (name + topics + optional editorias), Dar match (eight real
// stories, Sim/Não) and Pronto! → Criar primeiro post. Sources are picked
// automatically from the topics. No account is needed and no model is
// called anywhere in this flow.
export function Start() {
  const { t } = useT();
  const s = useStart();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [phase, setPhase] = useState<Phase>("about");
  const [name, setName] = useState("");
  const [about, setAbout] = useState("");
  const [niches, setNiches] = useState<string[]>([]);
  const [pubID, setPubID] = useState<number | null>(null);

  // one radar per account: a visitor who already has one never sees the
  // form — they go to it
  const pubs = useQuery({ queryKey: ["publications"], queryFn: api.publications });
  const existing = pubs.data?.[0];
  useEffect(() => {
    if (existing && pubID === null) nav(`/publications/${existing.id}/today`, { replace: true });
  }, [existing, pubID, nav]);

  // KLOX-161: the creator's email, confirmed by magic link, comes before
  // the radar is saved. The link may be opened in another tab or the
  // inbox, so the gate keeps asking the server until the claim lands.
  const ws = useQuery({
    queryKey: ["workspace"],
    queryFn: api.workspace,
    retry: false,
    refetchInterval: (q) => (q.state.data && !q.state.data.claimed ? 3000 : false),
  });
  const claimed = ws.data?.claimed === true;

  const create = useMutation({
    mutationFn: () => api.createRadar(name.trim(), about.trim(), niches),
    onSuccess: (res) => {
      setPubID(res.publication.id);
      qc.invalidateQueries({ queryKey: ["publications"] });
      setPhase("calibrate");
    },
    onError: (err) => {
      if (err instanceof ApiRequestError && err.body.code === "one_newsletter") {
        qc.fetchQuery({ queryKey: ["publications"], queryFn: api.publications })
          .then((list) => nav(list[0] ? `/publications/${list[0].id}` : "/publications", { replace: true }))
          .catch(() => nav("/publications", { replace: true }));
      }
    },
  });

  if (existing && pubID === null) {
    return <div className="grid flex-1 place-items-center py-24 text-muted" aria-busy="true">…</div>;
  }

  const stepIdx = phase === "about" ? 1 : phase === "calibrate" ? 2 : 3;
  const canGo = name.trim() !== "" && about.trim() !== "";
  return (
    <div className="mx-auto w-full max-w-2xl px-4 py-8">
      <StepCounter steps={s.step} current={stepIdx} />

      {phase === "about" && ws.data && !claimed && (
        <>
          <h1 className="mt-4 font-display text-2xl font-bold">{s.gateTitle}</h1>
          <p className="mt-1 text-sm text-muted">{s.gateHint}</p>
          <Card className="mt-6 p-5" data-testid="email-gate">
            <ClaimEmail />
          </Card>
        </>
      )}

      {phase === "about" && (claimed || ws.isError) && (
        <>
          <h1 className="mt-4 font-display text-2xl font-bold">{s.aboutTitle}</h1>
          <p className="mt-1 text-sm text-muted">{s.aboutHint}</p>
          <Card className="mt-6 p-5">
            <label className="text-xs font-semibold uppercase tracking-wide text-muted" htmlFor="radar-name">
              {s.nameLabel}
            </label>
            <input
              id="radar-name"
              value={name}
              maxLength={60}
              autoFocus
              onChange={(e) => setName(e.target.value)}
              placeholder={s.namePlaceholder}
              className="mt-2 w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
            />
            <label className="mt-4 block text-xs font-semibold uppercase tracking-wide text-muted" htmlFor="about">
              {s.aboutLabel}
            </label>
            <textarea
              id="about"
              value={about}
              maxLength={280}
              rows={3}
              onChange={(e) => setAbout(e.target.value)}
              placeholder={s.aboutPlaceholder}
              className="mt-2 w-full rounded border border-line bg-paper px-3 py-2 text-sm outline-none focus:border-teal"
            />
            <p className="mt-4 text-xs font-semibold uppercase tracking-wide text-muted">{s.nichesLabel}</p>
            <div className="mt-2 flex flex-wrap gap-2" role="group" aria-label={s.nichesLabel}>
              {NICHE_KEYS.map((k) => {
                const on = niches.includes(k);
                return (
                  <button
                    key={k}
                    type="button"
                    aria-pressed={on}
                    onClick={() => setNiches(on ? niches.filter((n) => n !== k) : [...niches, k])}
                    className={
                      "rounded-full border px-3 py-1 text-sm transition-colors " +
                      (on ? "border-teal bg-teal/10 font-medium text-teal" : "border-line text-muted hover:border-ink/40")
                    }
                  >
                    {s.niches[k]}
                  </button>
                );
              })}
            </div>
            {create.error && !(create.error instanceof ApiRequestError && create.error.body.code === "one_newsletter") && (
              <p className="mt-3 text-xs text-danger" role="alert">{errText(t, create.error)}</p>
            )}
            <div className="mt-5 flex justify-end">
              <Button disabled={!canGo || create.isPending} onClick={() => create.mutate()}>
                {create.isPending ? s.creating : s.next}
              </Button>
            </div>
          </Card>
        </>
      )}

      {phase === "calibrate" && pubID !== null && (
        <Calibrate pubID={pubID} s={s} onDone={() => setPhase("done")} />
      )}

      {phase === "done" && pubID !== null && (
        <Done pubID={pubID} s={s} name={name.trim()} topics={about.trim()} />
      )}
    </div>
  );
}

function fill(tpl: string, vars: Record<string, string | number>) {
  return Object.entries(vars).reduce((acc, [k, v]) => acc.replace(`{${k}}`, String(v)), tpl);
}

// The deck: one card at a time, Sim/Não by button or ← →. Two Não on
// stories sharing a topic ask once whether to hide that topic.
function Calibrate({ pubID, s, onDone }: { pubID: number; s: StartStrings; onDone: () => void }) {
  const [cards, setCards] = useState<PautaCard[] | null>(null);
  const [i, setI] = useState(0);
  const [asking, setAsking] = useState<string | null>(null);
  const [hiddenTopic, setHiddenTopic] = useState<string | null>(null);
  const noTopics = useRef<Record<string, number>>({});
  const decided = useRef<Set<string>>(new Set());
  const loaded = useRef(false);

  const deck = useMutation({
    mutationFn: () => api.calibration(pubID),
    onSuccess: (c) => setCards(c),
    onError: () => setCards([]),
  });
  useEffect(() => {
    if (loaded.current) return;
    loaded.current = true;
    deck.mutate();
  }, [deck]);

  const answer = useMutation({
    mutationFn: (v: { id: number; a: "yes" | "no" }) => api.calibrationAnswer(pubID, v.id, v.a),
  });
  const skip = useMutation({ mutationFn: (topic: string) => api.skipTopic(pubID, topic) });

  const card = cards?.[i];
  const reply = useCallback(
    (a: "yes" | "no") => {
      if (!cards || !cards[i]) return;
      const c = cards[i];
      answer.mutate({ id: c.story_id, a });
      if (a === "no") {
        for (const topic of c.topics) {
          noTopics.current[topic] = (noTopics.current[topic] ?? 0) + 1;
          if (noTopics.current[topic] >= 2 && !decided.current.has(topic) && asking === null) {
            decided.current.add(topic);
            setAsking(topic);
          }
        }
      }
      if (i + 1 >= cards.length) onDone();
      else setI(i + 1);
    },
    [cards, i, answer, asking, onDone],
  );

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null;
      if (el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable)) return;
      if (e.key === "ArrowLeft") reply("no");
      else if (e.key === "ArrowRight") reply("yes");
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [reply]);

  return (
    <>
      <h1 className="mt-4 font-display text-2xl font-bold">{s.calibTitle}</h1>
      <p className="mt-1 text-sm text-muted">{s.calibHint}</p>

      {cards === null && (
        <Card className="mt-6 p-5" aria-busy="true">
          <p className="text-sm text-muted">{s.loadingDeck}</p>
          <SkeletonLine />
          <SkeletonLine />
        </Card>
      )}

      {cards !== null && cards.length === 0 && (
        <Card className="mt-6 p-5">
          <p className="text-sm text-muted">{s.emptyDeck}</p>
          <div className="mt-4 flex justify-end">
            <Button onClick={onDone}>{s.next}</Button>
          </div>
        </Card>
      )}

      {asking && (
        <div className="mt-4 flex flex-wrap items-center gap-3 rounded border border-warn/50 bg-warn/10 px-4 py-3 text-sm" role="alertdialog" aria-label={fill(s.hidePrompt, { topic: asking })}>
          <span className="font-medium">{fill(s.hidePrompt, { topic: asking })}</span>
          <Button
            size="sm"
            onClick={() => {
              skip.mutate(asking);
              setHiddenTopic(asking);
              setAsking(null);
            }}
          >
            {s.hideYes}
          </Button>
          <Button size="sm" variant="outline" onClick={() => setAsking(null)}>
            {s.hideNo}
          </Button>
        </div>
      )}
      {hiddenTopic && !asking && (
        <p className="mt-4 text-xs text-muted" role="status">{fill(s.hidden, { topic: hiddenTopic })}</p>
      )}

      {card && cards && (
        <Card className="mt-6 p-5" data-testid="calibration-card">
          <p className="text-xs text-muted">{fill(s.progress, { n: i + 1, total: cards.length })}</p>
          <h2 className="mt-2 font-display text-xl font-semibold leading-snug">{card.title}</h2>
          <p className="mt-1 text-xs text-muted">
            {card.outlet}
            {card.outlets > 1 && ` · ${fill(s.outlets, { n: card.outlets })}`}
            {card.age_hours !== null && ` · ${fill(s.hoursAgo, { n: Math.round(card.age_hours) })}`}
          </p>
          {card.summary && <p className="mt-3 text-sm">{card.summary}</p>}
          <div className="mt-5 flex items-center gap-3">
            <Button variant="outline" className="flex-1" onClick={() => reply("no")}>
              ← {s.no}
            </Button>
            <Button className="flex-1" onClick={() => reply("yes")}>
              {s.yes} →
            </Button>
          </div>
          <div className="mt-3 flex items-center justify-between text-xs text-muted">
            <span className="hidden sm:inline">{s.keys}</span>
            <button type="button" className="underline hover:text-ink" onClick={onDone}>
              {s.skip}
            </button>
          </div>
        </Card>
      )}
    </>
  );
}

// Step 3: the Radar is ready. "Criar primeiro post" opens the list of
// today's news to tick (KLOX-168); the link goes to the same place.
function Done({ pubID, s, name, topics }: { pubID: number; s: StartStrings; name: string; topics: string }) {
  const nav = useNavigate();
  const today = useQuery({ queryKey: ["onboarding-today", pubID], queryFn: () => api.today(pubID) });
  const cards = today.data?.cards ?? [];
  const todayPath = `/publications/${pubID}/today`;
  return (
    <>
      <h1 className="mt-4 font-display text-2xl font-bold">{s.doneTitle}</h1>
      <p className="mt-1 text-sm text-muted">{s.doneHint}</p>
      <Card className="mt-6 p-5">
        <dl className="grid gap-3 text-sm">
          <div>
            <dt className="text-xs font-semibold uppercase tracking-wide text-muted">{s.doneRadar}</dt>
            <dd className="mt-0.5 font-medium">{name}</dd>
          </div>
          <div>
            <dt className="text-xs font-semibold uppercase tracking-wide text-muted">{s.doneTopics}</dt>
            <dd className="mt-0.5 flex flex-wrap gap-2">
              {topics.split(",").map((x) => x.trim()).filter(Boolean).map((x) => (
                <span key={x} className="rounded-full border border-line px-3 py-0.5">{x}</span>
              ))}
            </dd>
          </div>
        </dl>
        <p className="mt-4 text-sm" aria-live="polite">
          {today.isPending ? "…" : cards.length > 0 ? fill(s.doneMatches, { n: cards.length }) : s.doneNone}
        </p>
        <div className="mt-5 flex flex-wrap items-center justify-between gap-3">
          <Link to={todayPath} replace className="text-sm underline hover:text-ink">{s.seeToday}</Link>
          <Button disabled={cards.length === 0} onClick={() => nav(todayPath, { replace: true })}>
            {s.firstPost}
          </Button>
        </div>
      </Card>
    </>
  );
}
