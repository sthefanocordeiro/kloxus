import { useT } from "../i18n";

// Create-flow strings for the "paste a link" scrape flow (KLOX-99):
// only the /new page imports this module, so the strings stay out of
// the landing chunk — the shared i18n table is part of the 150 KiB
// budget (KLOX-16). The shared create.* table keeps the keys the whole
// onboarding step needs; these are the scrape-flow extras.
const en = {
  pasteLoading: "Reading the page…",
  pasteHint: "We read the page and propose a name, sections and an editorial line — you review and edit before anything is created.",
  extractedBadge: "From your link",
  simulatedBadge: "Simulated — no page was fetched",
  extractedStats: "{words} words extracted · {sections} sections proposed",
  extractedReview: "Review and edit below — it only becomes your radar when you create it.",
  warnThin: "The page had little readable text — the proposal may need extra editing.",
  warnGeneric: "No clear sections were found — the classic structure is proposed instead.",
};

const pt: typeof en = {
  pasteLoading: "Lendo a página…",
  pasteHint: "Lemos a página e propomos nome, seções e linha editorial — você revisa e edita antes de criar qualquer coisa.",
  extractedBadge: "Do seu link",
  simulatedBadge: "Demonstração — nenhuma página foi buscada",
  extractedStats: "{words} palavras extraídas · {sections} seções propostas",
  extractedReview: "Revise e edite abaixo — vira seu radar só quando você criar.",
  warnThin: "A página tinha pouco texto legível — a proposta pode precisar de mais edição.",
  warnGeneric: "Sem seções claras na página — a estrutura clássica foi proposta.",
};

export type CreateStrings = typeof en;

// useCreate returns the shared create.* table plus the scrape-flow
// strings, so call sites keep one `c.x` accessor.
export function useCreate() {
  const { t, lang } = useT();
  return {
    ...t.create,
    ...(lang === "pt" ? pt : en),
  };
}
