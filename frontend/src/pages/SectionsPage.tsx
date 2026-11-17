import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router-dom";
import { api } from "../api";
import { sectionTitle, useT, type Lang } from "../i18n";
import { Badge, Card } from "../ui";

// Seções (§3.1): the newsletter's sections as the creator defined them —
// name, capacity and the commentary rule. Reordering and the flexible
// builder arrive with KLOX-65.
export function SectionsPage() {
  const { t, lang } = useT();
  const { id } = useParams();
  const pub = useQuery({ queryKey: ["publication", id], queryFn: () => api.publication(id!) });

  const sections = pub.data?.publication.sections ?? [];
  return (
    <div className="mx-auto w-full max-w-3xl p-4 md:p-6">
      <div className="grid gap-4 sm:grid-cols-2">
        {sections.map((s, i) => (
          <Card key={i}>
            <div className="flex items-center justify-between gap-2">
              <h2 className="font-display text-base font-bold">{sectionTitle(lang as Lang, s.title)}</h2>
              <Badge tone="muted">{t.sectionsPage.items(s.items)}</Badge>
            </div>
            <p className="mt-2 text-xs text-muted">
              {s.commentary === "required"
                ? t.sectionsPage.commentary.required
                : s.commentary === "optional"
                  ? t.sectionsPage.commentary.optional
                  : t.sectionsPage.commentary.none}
            </p>
          </Card>
        ))}
      </div>
    </div>
  );
}
