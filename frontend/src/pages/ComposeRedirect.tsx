import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Button, Card } from "../ui";

// /compor?pub=<radar>&noticia=<story> is the deep link mailed by the
// daily briefing (KLOX-267): create the pauta with that story pre-linked
// and land straight in its editor. It is a redirect surface, not a
// composer of its own — the writing stays in /pauta/:id.
export function ComposeRedirect() {
  const [params] = useSearchParams();
  const nav = useNavigate();
  const qc = useQueryClient();
  const { t, lang } = useT();
  const pubID = Number(params.get("pub"));
  const storyID = Number(params.get("noticia"));
  const valid = Number.isInteger(pubID) && pubID > 0 && Number.isInteger(storyID) && storyID > 0;
  const started = useRef(false);

  const create = useMutation({
    mutationFn: () => api.pautaCreate(pubID, [storyID]),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["today", String(pubID)] });
      nav(`/pauta/${r.issue_id}`, { replace: true });
    },
  });

  useEffect(() => {
    if (!valid || started.current) return;
    started.current = true;
    create.mutate();
    // mutate once per deep-link mount — StrictMode replays effects and
    // must not create a second empty pauta
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [valid, pubID, storyID]);

  const pt = lang === "pt";
  if (!valid) {
    return (
      <div className="mx-auto max-w-xl py-10">
        <Card>
          <h1 className="mb-2 text-lg font-semibold">
            {pt ? "Link incompleto" : "Incomplete link"}
          </h1>
          <p className="mb-4 text-sm text-muted">
            {pt
              ? "Este link não diz qual notícia abrir. Volte ao e-mail e use o botão da notícia."
              : "This link does not say which story to open. Go back to the email and use the story button."}
          </p>
          <Link to="/publications">
            <Button variant="outline">{pt ? "Ir para o Radar" : "Go to Radar"}</Button>
          </Link>
        </Card>
      </div>
    );
  }
  if (create.isError) {
    return (
      <div className="mx-auto max-w-xl py-10">
        <Card>
          <h1 className="mb-2 text-lg font-semibold">
            {pt ? "Não foi possível abrir o post" : "The post could not be opened"}
          </h1>
          <p className="mb-4 text-sm text-muted">{errText(t, create.error)}</p>
          <Link to={`/publications/${pubID}/today`}>
            <Button variant="outline">{pt ? "Ver as notícias de hoje" : "See today's stories"}</Button>
          </Link>
        </Card>
      </div>
    );
  }
  return (
    <div className="grid flex-1 place-items-center py-16 text-muted" aria-busy="true">
      {pt ? "Abrindo seu post…" : "Opening your post…"}
    </div>
  );
}
