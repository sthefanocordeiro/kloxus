import { useT } from "../i18n";

// Hoje strings — only /today imports this, so they stay out of the
// landing chunk (KLOX-16 budget). KLOX-168: the list is for choosing.
const pt = {
  title: "Notícias de hoje",
  hint: "Marque as notícias que quer no seu texto. Sim e Não ensinam o radar.",
  loading: "Buscando as notícias…",
  empty: "Sem notícia nova agora — volte mais tarde ou revise suas fontes no Radar.",
  select: "Selecionar notícia",
  write: "Escrever",
  yes: "Sim",
  no: "Não",
  outlets: "{n} veículos",
  hoursAgo: "há {n} h",
  selected: "{n} notícias selecionadas",
  selected1: "1 notícia selecionada",
  build: "Montar pauta →",
  building: "Montando…",
  piecesTitle: "Pautas em andamento",
  noPieces: "Nenhuma pauta ainda. Marque notícias e clique em Montar pauta.",
  piece: "Pauta {n}",
  status: { draft: "Rascunho", reviewed: "Pronta", exported: "Exportada" } as Record<string, string>,
  stale: "A lista de notícias mudou — revise sua seleção e tente de novo.",
};

const en: typeof pt = {
  title: "Today's stories",
  hint: "Tick the stories you want in your text. Yes and No teach the radar.",
  loading: "Fetching the stories…",
  empty: "No new stories right now — come back later or review your sources in Radar.",
  select: "Select story",
  write: "Write",
  yes: "Yes",
  no: "No",
  outlets: "{n} outlets",
  hoursAgo: "{n} h ago",
  selected: "{n} stories selected",
  selected1: "1 story selected",
  build: "Build brief →",
  building: "Building…",
  piecesTitle: "Briefs in progress",
  noPieces: "No briefs yet. Tick stories and click Build brief.",
  piece: "Brief {n}",
  status: { draft: "Draft", reviewed: "Ready", exported: "Exported" },
  stale: "The story list changed — review your picks and try again.",
};

export function useToday() {
  const { lang } = useT();
  return lang === "pt" ? pt : en;
}
