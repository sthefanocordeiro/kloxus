import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, type NewsletterSection } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Button, Card, SkeletonLine } from "../ui";
import { StepCounter } from "../components/StepCounter";
import { SectionBuilder, totalStories, SECTION_LIMITS } from "../components/SectionBuilder";

// Seções (§3.1, KLOX-65): the creator's section builder — the same
// component onboarding uses. Edits save the publication's section
// definitions; an edition already in progress keeps its own, which the
// note below the builder explains.
export function SectionsPage() {
  const { t } = useT();
  const { id } = useParams();
  const nav = useNavigate();
  // onboarding flow (Sources → Voltar): shows the step counter and a
  // Voltar back to step 1 (KLOX-113)
  const onboarding = useSearchParams()[0].get("onboarding") === "1";
  const qc = useQueryClient();
  const pub = useQuery({ queryKey: ["publication", id], queryFn: () => api.publication(id!) });
  const [sections, setSections] = useState<NewsletterSection[] | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (pub.data && sections === null) setSections(pub.data.publication.sections);
  }, [pub.data, sections]);

  const save = useMutation({
    mutationFn: () => api.updatePublication(id!, { sections: sections! }),
    onSuccess: () => {
      setSaved(true);
      qc.invalidateQueries({ queryKey: ["publication", id] });
      if (onboarding) nav(`/publications/${id}/sources`);
    },
  });

  if (!pub.data || sections === null) {
    return <div className="p-6"><SkeletonLine /></div>;
  }
  const valid =
    sections.length > 0 &&
    totalStories(sections) <= SECTION_LIMITS.totalMax &&
    sections.every((s) => s.title.trim() !== "");

  return (
    <div className="mx-auto w-full max-w-4xl p-4 md:p-6">
      {onboarding && (
        <StepCounter
          className="mb-4"
          steps={t.steps.onboarding}
          current={2}
          onStep={(i) => nav(i === 0 ? `/publications/${id}/settings?onboarding=1` : `/publications/${id}/sources`)}
        />
      )}
      <Card className="p-5">
        <SectionBuilder
          value={sections}
          onChange={(s) => {
            setSections(s);
            setSaved(false);
          }}
        />
        <p className="mt-3 text-xs text-muted">{t.builder.nextEdition}</p>
        {save.error && <p className="mt-2 text-xs text-danger">{errText(t, save.error)}</p>}
        {saved && <p className="mt-2 text-xs font-medium text-teal">{t.sectionsPage.saved}</p>}
        <div className="mt-4 flex items-center justify-end gap-3">
          {onboarding && (
            <Button variant="outline" disabled={save.isPending} onClick={() => nav(`/publications/${id}/settings?onboarding=1`)}>
              {t.nav.back}
            </Button>
          )}
          <Button disabled={!valid || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? t.sectionsPage.saving : onboarding ? t.create.next : t.sectionsPage.save}
          </Button>
        </div>
      </Card>
    </div>
  );
}
