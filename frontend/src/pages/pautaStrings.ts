import { useT } from "../i18n";

// Escrever / Exportar strings (KLOX-168).
const pt = {
  loading: "Carregando…",
  title: "Escrever",
  nameLabel: "Nome da pauta",
  nameHint: "Clique para renomear",
  todayDest: "Notícias de hoje",
  hint: "Escreva do seu jeito sob cada notícia. Tudo é salvo sozinho.",
  opening: "Abertura (opcional)",
  openingHint: "Uma frase para abrir o texto…",
  text: "Seu comentário",
  comment: "Comentar",
  textHint: "Escreva aqui o que você quer dizer sobre esta notícia…",
  empty: "Nenhuma notícia na pauta. Adicione uma abaixo.",
  saving: "Salvando…",
  saved: "Salvo",
  saveError: "Não foi possível salvar — tentando de novo ao editar",
  up: "Subir notícia",
  down: "Descer notícia",
  remove: "Remover notícia",
  add: "+ Adicionar notícia",
  addTitle: "Notícias de hoje",
  addOne: "Adicionar",
  close: "Fechar",
  noMore: "Sem outras notícias de hoje.",
  toExport: "Ir para exportar",
  exportTitle: "Exportar",
  missing1: "1 notícia está sem texto. Você pode exportar assim mesmo.",
  missingN: "{n} notícias estão sem texto. Você pode exportar assim mesmo.",
  retry: "Tentar de novo",
};

const en: typeof pt = {
  loading: "Loading…",
  title: "Write",
  nameLabel: "Brief name",
  nameHint: "Click to rename",
  todayDest: "Today's stories",
  hint: "Write in your own words under each story. Everything saves itself.",
  opening: "Opening (optional)",
  openingHint: "A line to open the text…",
  text: "Your comment",
  comment: "Comment",
  textHint: "Write here what you want to say about this story…",
  empty: "No stories in this brief. Add one below.",
  saving: "Saving…",
  saved: "Saved",
  saveError: "Could not save — will retry on your next edit",
  up: "Move story up",
  down: "Move story down",
  remove: "Remove story",
  add: "+ Add story",
  addTitle: "Today's stories",
  addOne: "Add",
  close: "Close",
  noMore: "No other stories today.",
  toExport: "Go to export",
  exportTitle: "Export",
  missing1: "1 story has no text. You can export anyway.",
  missingN: "{n} stories have no text. You can export anyway.",
  retry: "Try again",
};

export function usePautaStrings() {
  const { lang } = useT();
  return lang === "pt" ? pt : en;
}
