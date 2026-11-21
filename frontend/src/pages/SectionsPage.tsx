import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { api, type NewsletterSection } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Button, Card, SkeletonLine } from "../ui";
import { SectionBuilder, totalStories, SECTION_LIMITS } from "../components/SectionBuilder";

// Seções (§3.1, KLOX-65): the creator's section builder — the same
// component onboarding uses. Edits save the publication's section
// definitions; an edition already in progress keeps its own, which the
// note below the builder explains.
export function SectionsPage() {
  const { t } = useT();
  const { id } = useParams();
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
        <div className="mt-4 flex justify-end">
          <Button disabled={!valid || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? t.sectionsPage.saving : t.sectionsPage.save}
          </Button>
        </div>
      </Card>
    </div>
  );
}
