import { useT } from "../i18n";

// Board-only suggestion-card strings (KLOX-95): the article-type chip,
// the hidden-suggestions toggle and the filter badges render only on
// the board, so they stay out of the landing chunk — the shared i18n
// table is part of the 150 KiB budget (KLOX-16, same split as
// readerStrings.ts / createStrings.ts).
const en = {
  kinds: {
    noticia: "news", analise: "analysis", tutorial: "tutorial",
    forum: "forum", lancamento: "launch", lista: "list",
    publieditorial: "sponsored", entretenimento: "entertainment",
    esporte: "sports", efemeride: "almanac", outro: "other",
    regulacao: "regulation", opiniao: "opinion",
  },
  hiddenBench: (n: number) => `${n} hidden — off-topic or another language`,
  showHidden: "show",
  hideHidden: "hide",
  badgeOffTopic: "off-topic",
};

const pt: typeof en = {
  kinds: {
    noticia: "notícia", analise: "análise", tutorial: "passo a passo",
    forum: "fórum", lancamento: "lançamento", lista: "lista",
    publieditorial: "publieditorial", entretenimento: "entretenimento",
    esporte: "esporte", efemeride: "efeméride", outro: "outro",
    regulacao: "regulação", opiniao: "opinião",
  },
  hiddenBench: (n: number) => `${n} ${n === 1 ? "oculta" : "ocultas"} — fora do tema ou outro idioma`,
  showHidden: "mostrar",
  hideHidden: "ocultar",
  badgeOffTopic: "fora do tema",
};

// useBoardStrings merges the shared board.* table with the KLOX-95
// suggestion-card strings, so call sites keep one `board.x` accessor.
export function useBoardStrings() {
  const { t, lang } = useT();
  return {
    ...t.board,
    ...(lang === "pt" ? pt : en),
  };
}