import { useT } from "../i18n";

// Hoje strings — only /today imports this, so they stay out of the
// landing chunk (KLOX-16 budget). KLOX-168: the list is for choosing.
const pt = {
  title: "Notícias de hoje",
  hint: "Clique numa notícia para colocá-la no seu texto. Os botões de polegar só ensinam o radar.",
  loading: "Buscando as notícias…",
  empty: "Sem notícia nova agora — volte mais tarde ou revise suas fontes no Radar.",
  select: "Selecionar notícia",
  more: "Mais disso",
  less: "Menos disso",
  outlets: "{n} veículos",
  hoursAgo: "há {n} h",
  selected: "{n} notícias selecionadas",
  selected1: "1 notícia selecionada",
  build: "Montar pauta →",
  building: "Juntando as {n} notícias…",
  building1: "Juntando a notícia…",
  buildingEditor: "Abrindo o editor…",
  piecesTitle: "Pautas em andamento",
  noPieces: "Nenhuma pauta ainda. Clique nas notícias que quiser e use Montar pauta.",
  piece: "Pauta {n}",
  status: { draft: "Rascunho", reviewed: "Pronta", exported: "Exportada" } as Record<string, string>,
  statusEdited: "Editada desde a exportação",
  stale: "A lista de notícias mudou — revise sua seleção e tente de novo.",
};

const en: typeof pt = {
  title: "Today's stories",
  hint: "Click a story to put it in your text. The thumbs buttons only teach the radar.",
  loading: "Fetching the stories…",
  empty: "No new stories right now — come back later or review your sources in Radar.",
  select: "Select story",
  more: "More of this",
  less: "Less of this",
  outlets: "{n} outlets",
  hoursAgo: "{n} h ago",
  selected: "{n} stories selected",
  selected1: "1 story selected",
  build: "Build brief →",
  building: "Putting the {n} stories together…",
  building1: "Putting the story together…",
  buildingEditor: "Opening the editor…",
  piecesTitle: "Briefs in progress",
  noPieces: "No briefs yet. Click the stories you want and hit Build brief.",
  piece: "Brief {n}",
  status: { draft: "Draft", reviewed: "Ready", exported: "Exported" },
  statusEdited: "Edited since export",
  stale: "The story list changed — review your picks and try again.",
};

export function useToday() {
  const { lang } = useT();
  return lang === "pt" ? pt : en;
}
