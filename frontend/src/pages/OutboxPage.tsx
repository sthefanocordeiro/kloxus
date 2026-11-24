import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router-dom";
import { api, type OutboxEmail } from "../api";
import { useT } from "../i18n";
import { cn } from "../lib/cn";
import { fmtDate } from "../lib/fmt";
import { Badge, Card, SkeletonLine } from "../ui";
import { useState } from "react";

const KIND_TONE: Record<string, "teal" | "ink" | "muted"> = {
  issue: "ink",
  test: "teal",
  magic_link: "muted",
  unsubscribe: "muted",
};

// The workspace outbox: every email the demo "sent" — issue emails,
// tests, the magic link — with a rendered preview of each.
export function OutboxPage() {
  const { id = "" } = useParams();
  const { t, lang } = useT();
  const emails = useQuery({ queryKey: ["outbox", id], queryFn: () => api.outbox(Number(id)) });
  const [open, setOpen] = useState<number | null>(null);

  const kindLabel = (k: string) =>
    ((t.outboxPage as Record<string, string>)[k] ?? k) as string;

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-8">
      <h1 className="mb-6 font-display text-2xl font-bold">{t.outbox}</h1>

      {emails.data?.length === 0 && <p className="text-sm text-muted">{t.outboxPage.empty}</p>}

      <div className="grid gap-2">
        {emails.data?.map((e) => (
          <Card key={e.id} className="p-0 overflow-hidden">
            <button
              className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-paper-raised"
              onClick={() => setOpen(open === e.id ? null : e.id)}
              aria-expanded={open === e.id}
            >
              <Badge tone={KIND_TONE[e.kind] ?? "muted"}>{kindLabel(e.kind)}</Badge>
              <span className="min-w-0 flex-1 truncate text-sm font-medium">{e.subject}</span>
              <span className="text-xs text-muted">{e.to}</span>
              <span className="text-xs text-muted">{fmtDate(e.created_at, lang, true)}</span>
            </button>
            {open === e.id && <EmailPreview id={e.id} />}
          </Card>
        ))}
      </div>
    </div>
  );
}

function EmailPreview({ id }: { id: number }) {
  const q = useQuery({ queryKey: ["outboxEmail", id], queryFn: () => api.outboxEmail(id) });
  const e: OutboxEmail | undefined = q.data;
  if (!e) return <div className="border-t border-line px-4 py-3"><SkeletonLine className="h-3 w-32" /></div>;
  // A fetched same-origin frame, not a srcdoc one — srcdoc previews
  // painted blank intermittently at narrow widths (KLOX-100). The
  // server wraps the body in a strict CSP sandbox; the attribute here
  // is a second layer. Markdown-only mails render inside the same
  // frame, so every email previews identically at any viewport.
  return (
    <div className="border-t border-line">
      {e.body_html || e.body_md ? (
        <iframe
          title={e.subject}
          sandbox=""
          src={`/outbox/${id}/html`}
          className={cn("h-[420px] w-full bg-white")}
        />
      ) : (
        <pre className="max-h-96 overflow-auto whitespace-pre-wrap px-4 py-3 text-xs">{e.body_md}</pre>
      )}
    </div>
  );
}
