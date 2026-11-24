import { useT } from "../i18n";

// Escrever / Exportar strings (KLOX-168).
const pt = {
  loading: "Carregando…",
  title: "Escrever",
  todayDest: "Notícias de hoje",
  hint: "Escreva do seu jeito sob cada notícia. Tudo é salvo sozinho.",
  opening: "Abertura (opcional)",
  openingHint: "Uma frase para abrir o texto…",
  text: "Seu texto",
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
};

const en: typeof pt = {
  loading: "Loading…",
  title: "Write",
  todayDest: "Today's news",
  hint: "Write in your own words under each story. Everything saves itself.",
  opening: "Opening (optional)",
  openingHint: "A line to open the text…",
  text: "Your text",
  textHint: "Write here what you want to say about this story…",
  empty: "No stories in the pauta. Add one below.",
  saving: "Saving…",
  saved: "Saved",
  saveError: "Could not save — will retry on your next edit",
  up: "Move story up",
  down: "Move story down",
  remove: "Remove story",
  add: "+ Add story",
  addTitle: "Today's news",
  addOne: "Add",
  close: "Close",
  noMore: "No other stories today.",
  toExport: "Go to export",
  exportTitle: "Export",
  missing1: "1 story has no text. You can export anyway.",
  missingN: "{n} stories have no text. You can export anyway.",
};

export function usePautaStrings() {
  const { lang } = useT();
  return lang === "pt" ? pt : en;
}
