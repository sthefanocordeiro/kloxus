import { useT } from "../i18n";

// Hoje strings (KLOX-123 slice 2) — only /today imports this, so they
// stay out of the landing chunk (KLOX-16 budget).
const pt = {
  title: "Pauta de hoje",
  hint: "As notícias que mais combinam com seu radar. Sim e Não ensinam o radar; Escrever abre uma peça.",
  loading: "Montando a pauta…",
  empty: "Sem pauta nova agora — volte mais tarde ou revise suas fontes no Radar.",
  write: "Escrever",
  yes: "Sim",
  no: "Não",
  outlets: "{n} veículos",
  hoursAgo: "há {n} h",
  piecesTitle: "Peças em andamento",
  noPieces: "Nenhuma peça ainda. Escolha uma notícia e clique em Escrever.",
  piece: "Peça {n}",
  status: { draft: "Rascunho", reviewed: "Revisada", exported: "Exportada" } as Record<string, string>,
};

const en: typeof pt = {
  title: "Today's pauta",
  hint: "The stories that best fit your radar. Yes and No teach the radar; Write opens a piece.",
  loading: "Building the pauta…",
  empty: "No new pauta right now — come back later or review your sources in Radar.",
  write: "Write",
  yes: "Yes",
  no: "No",
  outlets: "{n} outlets",
  hoursAgo: "{n} h ago",
  piecesTitle: "Pieces in progress",
  noPieces: "No pieces yet. Pick a story and click Write.",
  piece: "Piece {n}",
  status: { draft: "Draft", reviewed: "Reviewed", exported: "Exported" },
};

export function useToday() {
  const { lang } = useT();
  return lang === "pt" ? pt : en;
}
