import { useT } from "../i18n";

// Hoje strings (KLOX-123 slice 2) — only /today imports this, so they
// stay out of the landing chunk (KLOX-16 budget).
const pt = {
  title: "Notícias de hoje",
  hint: "Marque as notícias que entram e clique em Escrever.",
  loading: "Montando as notícias…",
  empty: "Sem notícias novas agora — volte mais tarde ou revise suas fontes no Radar.",
  write: "Escrever",
  yes: "Sim",
  no: "Não",
  outlets: "{n} veículos",
  hoursAgo: "há {n} h",
  piecesTitle: "Pautas em andamento",
  noPieces: "Nenhuma pauta ainda. Escolha uma notícia e clique em Escrever.",
  piece: "Pauta {n}",
  status: { draft: "Rascunho", reviewed: "Pronta", exported: "Exportada" } as Record<string, string>,
};

const en: typeof pt = {
  title: "Today's stories",
  hint: "Mark the stories that go in and click Write.",
  loading: "Building today's stories…",
  empty: "No new stories right now — come back later or review your sources in Radar.",
  write: "Write",
  yes: "Yes",
  no: "No",
  outlets: "{n} outlets",
  hoursAgo: "{n} h ago",
  piecesTitle: "Briefs in progress",
  noPieces: "No briefs yet. Pick a story and click Write.",
  piece: "Brief {n}",
  status: { draft: "Draft", reviewed: "Ready", exported: "Exported" },
};

export function useToday() {
  const { lang } = useT();
  return lang === "pt" ? pt : en;
}
