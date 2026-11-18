import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { Inbox } from "lucide-react";
import { api } from "../api";
import { useT } from "../i18n";
import { Badge, Card } from "../ui";

// Leitores (§3.1): confirmed-reader and paying-subscriber counts, plus
// the outbox entry point where subscription links land.
export function ReadersPage() {
  const { t } = useT();
  const { id } = useParams();
  const pubID = Number(id);
  const reader = useQuery({ queryKey: ["reader", pubID], queryFn: () => api.reader(pubID) });

  const r = reader.data;
  return (
    <div className="mx-auto w-full max-w-3xl p-4 md:p-6">
      <div className="grid gap-4 sm:grid-cols-2">
        <Card>
          <p className="text-3xl font-bold">{r?.confirmed ?? r?.readers ?? "—"}</p>
          <p className="mt-1 text-sm text-muted">{t.readersPage.confirmed(r?.confirmed ?? r?.readers ?? 0)}</p>
        </Card>
        <Card>
          <p className="text-3xl font-bold">{r?.subscribers ?? "—"}</p>
          <p className="mt-1 text-sm text-muted">{t.readersPage.subscribers(r?.subscribers ?? 0)}</p>
        </Card>
      </div>
      {!r?.confirmed && !r?.readers && (
        <p className="mt-6 text-sm text-muted">{t.readersPage.empty}</p>
      )}
      <Link
        to={`/publications/${pubID}/outbox`}
        className="mt-6 inline-flex items-center gap-2 text-sm font-medium text-teal hover:underline"
      >
        <Inbox size={15} aria-hidden />
        {t.readersPage.outboxLink}
        <Badge tone="muted" className="ml-1">→</Badge>
      </Link>
    </div>
  );
}
