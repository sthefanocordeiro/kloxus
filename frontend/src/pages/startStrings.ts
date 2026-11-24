import { useT } from "../i18n";

// Onboarding strings (KLOX-123, reshaped by KLOX-159 / D3-D4: three
// steps — Criar Radar, Dar match, Pronto!). Only /start imports this
// module, so it stays out of the landing chunk (KLOX-16 budget).
const pt = {
  gateTitle: "Confirme seu e-mail para criar o radar",
  gateHint: "Enviamos um link mágico — sem senha. É também assim que você entra na lista do convite do lançamento.",
  step: ["Criar Radar", "Dar match", "Pronto!"],
  aboutTitle: "Criar seu Radar",
  aboutHint: "Dê um nome e liste os temas. As fontes são escolhidas automaticamente.",
  nameLabel: "Nome do Radar",
  namePlaceholder: "Fintech para bancários",
  aboutLabel: "Temas (separados por vírgula)",
  aboutPlaceholder: "Pix, open finance, Drex, regulação do BC",
  nichesLabel: "Editorias (opcional)",
  niches: {
    fintech: "Fintech", cripto: "Cripto", regulacao: "Regulação financeira", ia: "IA",
    startups: "Startups", tecnologia: "Tecnologia", mercado: "Mercado",
    economia: "Economia", negocios: "Negócios", carreira: "Carreira",
  } as Record<string, string>,
  next: "Continuar",
  creating: "Criando seu Radar…",
  calibTitle: "Dar match",
  calibHint: "Notícias reais das últimas 48 h. Diga se entram na sua pauta — cada resposta ensina o Radar.",
  loadingDeck: "Buscando notícias reais…",
  yes: "Sim",
  no: "Não",
  skip: "Pular",
  keys: "Atalhos: ← não · → sim",
  progress: "{n} de {total}",
  outlets: "{n} veículos",
  hidePrompt: "Esconder {topic}?",
  hideYes: "Esconder",
  hideNo: "Manter",
  hidden: "{topic} escondido da sua pauta.",
  emptyDeck: "Ainda não há notícias do seu tema para dar match — o Radar aprende conforme você usa.",
  doneTitle: "Pronto!",
  doneHint: "Seu Radar está no ar e já aprendeu com suas respostas.",
  doneRadar: "Radar",
  doneTopics: "Temas",
  doneMatches: "{n} notícias combinam com seu Radar hoje.",
  doneNone: "Ainda não há notícias do seu tema hoje — o Radar segue buscando.",
  firstPost: "Criar primeiro post",
  opening: "Abrindo…",
  seeToday: "Ver a pauta de hoje",
  hoursAgo: "há {n} h",
};

const en: typeof pt = {
  gateTitle: "Confirm your email to create your radar",
  gateHint: "We send a magic link — no password. It is also how you get on the launch invitation list.",
  step: ["Create Radar", "Match", "Done!"],
  aboutTitle: "Create your Radar",
  aboutHint: "Give it a name and list the topics. Sources are picked for you.",
  nameLabel: "Radar name",
  namePlaceholder: "Fintech for bankers",
  aboutLabel: "Topics (comma separated)",
  aboutPlaceholder: "Pix, open finance, Drex, central bank rules",
  nichesLabel: "Sections (optional)",
  niches: {
    fintech: "Fintech", cripto: "Crypto", regulacao: "Financial regulation", ia: "AI",
    startups: "Startups", tecnologia: "Technology", mercado: "Markets",
    economia: "Economy", negocios: "Business", carreira: "Career",
  },
  next: "Continue",
  creating: "Creating your Radar…",
  calibTitle: "Match",
  calibHint: "Real stories from the last 48 h. Say whether they belong in your pauta — each answer teaches the Radar.",
  loadingDeck: "Fetching real stories…",
  yes: "Yes",
  no: "No",
  skip: "Skip",
  keys: "Shortcuts: ← no · → yes",
  progress: "{n} of {total}",
  outlets: "{n} outlets",
  hidePrompt: "Hide {topic}?",
  hideYes: "Hide",
  hideNo: "Keep",
  hidden: "{topic} hidden from your pauta.",
  emptyDeck: "No stories on your topic to match yet — the Radar learns as you use it.",
  doneTitle: "Done!",
  doneHint: "Your Radar is live and already learned from your answers.",
  doneRadar: "Radar",
  doneTopics: "Topics",
  doneMatches: "{n} stories match your Radar today.",
  doneNone: "No stories on your topic today yet — the Radar keeps looking.",
  firstPost: "Create first post",
  opening: "Opening…",
  seeToday: "See today's pauta",
  hoursAgo: "{n} h ago",
};

export type StartStrings = typeof pt;

export const NICHE_KEYS = [
  "economia", "negocios", "carreira", "tecnologia", "mercado", "fintech", "cripto", "regulacao", "ia", "startups",
];

export function useStart(): StartStrings {
  const { lang } = useT();
  return lang === "pt" ? pt : en;
}
