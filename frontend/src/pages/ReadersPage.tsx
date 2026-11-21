import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { Inbox } from "lucide-react";
import { useRef, useState } from "react";
import { api } from "../api";
import { useReadersPage } from "./readersPageStrings";
import { Button, Card } from "../ui";

// Leitores (§3.1): confirmed-reader and paying-subscriber counts, the
// copyable subscribe link (KLOX-102) and the outbox entry point where
// subscription links land.
export function ReadersPage() {
  const t = useReadersPage();
  const { id } = useParams();
  const pubID = Number(id);
  const reader = useQuery({ queryKey: ["reader", pubID], queryFn: () => api.reader(pubID) });

  const r = reader.data;
  return (
    <div className="mx-auto w-full max-w-3xl p-4 md:p-6">
      <div className="grid gap-4 sm:grid-cols-2">
        <Card>
          <p className="text-3xl font-bold">{r?.confirmed ?? r?.readers ?? "—"}</p>
          <p className="mt-1 text-sm text-muted">{t.confirmed(r?.confirmed ?? r?.readers ?? 0)}</p>
        </Card>
        <Card>
          <p className="text-3xl font-bold">{r?.subscribers ?? "—"}</p>
          <p className="mt-1 text-sm text-muted">{t.subscribers(r?.subscribers ?? 0)}</p>
        </Card>
        {r?.public_url && <SubscribeLink url={r.public_url} />}
      </div>
      {/* the API always keeps one unconfirmed demo reader row, so the
          meaningful empty state is "no CONFIRMED readers yet" (KLOX-68);
          gated on r so it never flashes while the page loads */}
      {r && !(r.confirmed ?? r.readers) && (
        <p className="mt-6 text-sm text-muted">{t.empty}</p>
      )}
      <Link
        to={`/publications/${pubID}/outbox`}
        className="mt-6 inline-flex items-center gap-2 text-sm font-medium text-teal hover:underline"
      >
        <Inbox size={15} aria-hidden />
        {t.outboxLink}
      </Link>
    </div>
  );
}

// The copyable subscribe link (KLOX-102): the stable public newsletter
// page — the invite path until list import ships (follow-up recorded on
// the issue). The readonly input keeps a manual copy possible when the
// async clipboard is unavailable or denied.
function SubscribeLink({ url }: { url: string }) {
  const t = useReadersPage();
  const inputRef = useRef<HTMLInputElement>(null);
  const [copied, setCopied] = useState(false);
  const copy = () => {
    if (!navigator.clipboard?.writeText) {
      inputRef.current?.select();
      return;
    }
    navigator.clipboard.writeText(url).then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 2000);
      },
      () => inputRef.current?.select(),
    );
  };
  return (
    <Card className="sm:col-span-2" aria-live="polite">
      <p className="text-sm font-semibold">{t.subscribeLink}</p>
      <p className="mt-1 text-xs text-muted">{t.subscribeLinkHint}</p>
      <div className="mt-3 flex items-center gap-2">
        <input
          ref={inputRef}
          readOnly
          value={url}
          onFocus={(e) => e.currentTarget.select()}
          aria-label={t.subscribeLink}
          className="w-full min-w-0 rounded border border-line bg-paper px-2 py-1.5 text-sm text-ink"
        />
        <Button size="sm" variant="outline" onClick={copy}>
          {copied ? t.copied : t.copy}
        </Button>
      </div>
    </Card>
  );
}
