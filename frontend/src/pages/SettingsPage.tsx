import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { BookOpen, Inbox } from "lucide-react";
import { api } from "../api";
import { useT, type T } from "../i18n";
import { Card } from "../ui";

// Configurações (§3.1): the publication's editorial identity, plus links
// to the guide the rejections teach and the outbox where email lives.
export function SettingsPage() {
  const { t } = useT();
  const { id } = useParams();
  const pub = useQuery({ queryKey: ["publication", id], queryFn: () => api.publication(id!) });

  const p = pub.data?.publication;
  if (!p) return <div className="p-6 text-sm text-muted">…</div>;

  const rows: [string, string][] = [
    [t.settingsPage.name, p.name],
    [t.settingsPage.topic, p.topic],
    [t.settingsPage.audience, p.audience],
    [t.settingsPage.editorialLine, p.editorial_line],
    [t.settingsPage.tone, p.tone],
    [t.settingsPage.frequency, t.frequency[p.frequency as keyof T["frequency"]] ?? p.frequency],
    [
      t.settingsPage.price,
      p.paywall.enabled ? p.paywall.price : t.settingsPage.free,
    ],
  ];
  return (
    <div className="mx-auto w-full max-w-3xl space-y-4 p-4 md:p-6">
      <Card>
        <dl className="grid gap-3 sm:grid-cols-2">
          {rows.map(([k, v]) => (
            <div key={k}>
              <dt className="text-xs font-semibold uppercase tracking-wide text-muted">{k}</dt>
              <dd className="mt-0.5 text-sm">{v || "—"}</dd>
            </div>
          ))}
        </dl>
      </Card>
      <Card className="flex flex-wrap gap-4">
        <Link to={`/publications/${p.id}/guide`} className="inline-flex items-center gap-2 text-sm font-medium text-teal hover:underline">
          <BookOpen size={15} aria-hidden />
          {t.settingsPage.guideLink}
        </Link>
        <Link to={`/publications/${p.id}/outbox`} className="inline-flex items-center gap-2 text-sm font-medium text-teal hover:underline">
          <Inbox size={15} aria-hidden />
          {t.settingsPage.outboxLink}
        </Link>
      </Card>
    </div>
  );
}
