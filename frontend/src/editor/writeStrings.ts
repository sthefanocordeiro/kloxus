import { useT } from "../i18n";

// Write-in-document strings (KLOX-96): only the issue page's chunk
// imports this module, so they stay out of the landing chunk — the
// shared i18n table is part of the 150 KiB budget (KLOX-16, same
// split as readerStrings.ts / createStrings.ts).
const en = {
  addLine: "Add a sentence",
  writing: (done: number, total: number) => `Writing ${done} of ${total}`,
  editHint: "Click for actions · double-click to edit",
  useFact: "insert as a sentence — rewrite it your way",
  prevPauta: "Previous story",
  nextPauta: "Next story",
  position: (n: number, m: number) => `Story ${n} of ${m}`,
};

const pt: typeof en = {
  addLine: "Adicionar frase",
  writing: (done: number, total: number) => `Escrevendo ${done} de ${total}`,
  editHint: "Clique para ações · dois cliques para editar",
  useFact: "usar como frase — reescreva do seu jeito",
  prevPauta: "Pauta anterior",
  nextPauta: "Próxima pauta",
  position: (n: number, m: number) => `Pauta ${n} de ${m}`,
};

export function useWrite() {
  const { lang } = useT();
  return lang === "pt" ? pt : en;
}
