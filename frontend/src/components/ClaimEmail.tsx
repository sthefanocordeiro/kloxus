import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useT } from "../i18n";
import { errText } from "../lib/errors";
import { Button } from "../ui";

// "Salvar com seu e-mail" (§3.4): the single place the app asks for the
// creator's address — inside the account menu and inside the send flow
// when a real send needs it. It mails one magic link; outbox mode
// returns the link itself so the demo can open it.
export function ClaimEmail({ onDone }: { onDone?: () => void }) {
  const { t } = useT();
  const qc = useQueryClient();
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState<{ email: string; claim_url?: string } | null>(null);
  const claim = useMutation({
    mutationFn: () => api.claim(email.trim()),
    onSuccess: (r) => {
      setSent({ email: r.email, claim_url: r.claim_url });
      qc.invalidateQueries({ queryKey: ["workspace"] });
      onDone?.();
    },
  });
  if (sent) {
    return (
      <p className="text-sm text-muted">
        {t.claim.sent(sent.email)}
        {sent.claim_url && (
          <a href={sent.claim_url} className="ml-2 font-semibold text-teal underline">
            {t.claim.openLink}
          </a>
        )}
      </p>
    );
  }
  return (
    <form
      className="flex items-center gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (email.trim()) claim.mutate();
      }}
    >
      <input
        type="email"
        required
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        placeholder={t.claim.emailPlaceholder}
        aria-label={t.claim.emailPlaceholder}
        className="min-w-0 flex-1 rounded-[var(--radius-input)] border border-line bg-paper px-3 py-2 text-sm text-ink"
      />
      <Button size="sm" disabled={claim.isPending}>
        {claim.isPending ? t.claim.sending : t.claim.action}
      </Button>
      {claim.error && <span className="text-xs text-danger">{errText(t, claim.error)}</span>}
    </form>
  );
}
