import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { api } from "../api";
import { TemplateBuilder, toRows, type TemplateRow } from "../components/TemplateBuilder";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Button, Card, SkeletonLine } from "../ui";

// Modelo (KLOX-72): edit the newsletter's default edition template —
// the ordered block list every new edition materializes. Template
// edits apply to editions created after the save; in-progress
// editions keep their own instantiated copy.
export function TemplatePage() {
  const { t } = useT();
  const { id } = useParams();
  const qc = useQueryClient();
  const pubQ = useQuery({ queryKey: ["publication", id], queryFn: () => api.publication(id!) });
  const tplQ = useQuery({ queryKey: ["template", id], queryFn: () => api.template(Number(id)) });
  const [rows, setRows] = useState<TemplateRow[] | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (tplQ.data && !rows) setRows(toRows(tplQ.data.blocks));
  }, [tplQ.data, rows]);

  const save = useMutation({
    mutationFn: () =>
      api.saveTemplate(
        Number(id),
        (rows ?? []).map(({ id: _id, ...b }) => b),
        tplQ.data?.version ?? "",
      ),
    onSuccess: (tpl) => {
      setSaved(true);
      qc.setQueryData(["template", id], tpl);
      setRows(toRows(tpl.blocks));
    },
  });

  const pub = pubQ.data?.publication;
  if (!pub || !tplQ.data || !rows) return <div className="p-6"><SkeletonLine /></div>;

  return (
    <div className="mx-auto w-full max-w-3xl space-y-4 p-4 md:p-6">
      <Card className="p-5">
        <h1 className="font-display text-lg font-bold">{t.templatePage.title}</h1>
        <p className="mt-1 text-sm text-muted">{t.templatePage.intro}</p>
        <div className="mt-4">
          <TemplateBuilder
            value={rows}
            onChange={(r) => { setRows(r); setSaved(false); }}
            sections={pub.sections}
            pubID={pub.id}
          />
        </div>
        {save.error && <p className="mt-3 text-xs text-danger">{errText(t, save.error)}</p>}
        {saved && <p className="mt-3 text-xs font-medium text-teal">{t.templatePage.saved}</p>}
        <div className="mt-5">
          <Button disabled={save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? t.settingsPage.saving : t.templatePage.save}
          </Button>
        </div>
      </Card>
    </div>
  );
}
