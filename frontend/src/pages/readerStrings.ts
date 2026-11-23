import { useT } from "../i18n";

// Reader-page payment strings for the paid checkout (KLOX-13): only
// the reader routes import this module, so the strings stay out of
// the landing chunk — the shared i18n table is part of the 150 KiB
// budget (KLOX-16, same split as createStrings.ts).
const en = {
  payEmail: "Email for the access link",
  payAction: "Subscribe with PIX",
  payStarting: "Opening checkout…",
  testMode: "Test mode — no real charge",
  simMode: "Simulated payment — no checkout configured",
  payPending: "Waiting for payment confirmation…",
  payDone: "Payment confirmed — your access link is on its way to your inbox.",
  payOpen: "Open my subscriber access",
  payExpired: "This checkout expired — start a new one.",
  alreadyPaid: "This address already has an active subscription.",
  // the /p/{slug} newsletter landing page (KLOX-102)
  editionsArchive: "Pieces",
  latestBadge: "Latest",
  noEditions: "No pieces out yet — subscribe to get the next one.",
  newsletterPaid: (price: string) => `${price} · paid sections are for subscribers.`,
};

const pt: typeof en = {
  payEmail: "E-mail para o link de acesso",
  payAction: "Assinar com PIX",
  payStarting: "Abrindo o checkout…",
  testMode: "Modo de teste — sem cobrança real",
  simMode: "Pagamento de demonstração — nenhuma cobrança real",
  payPending: "Aguardando a confirmação do pagamento…",
  payDone: "Pagamento confirmado — o link de acesso está a caminho do seu e-mail.",
  payOpen: "Abrir meu acesso de assinante",
  payExpired: "Este checkout expirou — comece um novo.",
  alreadyPaid: "Este endereço já tem uma assinatura ativa.",
  // a página pública /p/{slug} (KLOX-102)
  editionsArchive: "Peças",
  latestBadge: "Mais recente",
  noEditions: "Nenhuma peça publicada ainda — inscreva-se para receber a próxima.",
  newsletterPaid: (price: string) => `${price} · seções pagas são para assinantes.`,
};

export type ReaderStrings = typeof en;

// useReader returns the shared reader.* table plus the checkout
// strings, so call sites keep one `r.x` accessor.
export function useReader() {
  const { t, lang } = useT();
  return {
    ...t.reader,
    ...(lang === "pt" ? pt : en),
  };
}
