import { useT } from "../i18n";

// Onboarding-from-zero strings (KLOX-123). Only /start imports this
// module, so it stays out of the landing chunk (KLOX-16 budget).
const pt = {
  step: ["Seu tema", "Calibrar", "Fontes"],
  aboutTitle: "Sobre o que você fala?",
  aboutHint: "Uma frase basta. A gente monta seu radar e busca a pauta de hoje.",
  aboutPlaceholder: "fintech, Pix e regulação para quem trabalha em banco",
  aboutLabel: "Seu tema",
  nichesLabel: "Ou escolha áreas",
  niches: {
    fintech: "Fintech", cripto: "Cripto", regulacao: "Regulação financeira", ia: "IA",
    startups: "Startups", tecnologia: "Tecnologia", mercado: "Mercado",
    politica: "Política", carreira: "Carreira e RH", geral: "Notícias gerais",
  } as Record<string, string>,
  next: "Continuar",
  creating: "Criando seu radar…",
  calibTitle: "Calibrar",
  calibHint: "Notícias reais das últimas 48 h. Diga se entram na sua pauta — cada resposta ensina o radar.",
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
  emptyDeck: "Ainda não há notícias novas para calibrar — seu radar aprende conforme você usa.",
  sourcesTitle: "Fontes",
  sourcesHint: "Já escolhemos pelas suas áreas. Desmarque o que não quiser — fontes próprias ficam em Configurações.",
  removed: "Removida",
  finish: "Ver a pauta de hoje",
  finishing: "Abrindo…",
  noSources: "Nenhuma fonte selecionada.",
  hoursAgo: "há {n} h",
  errAbout: "Escreva o tema ou escolha uma área.",
};

const en: typeof pt = {
  step: ["Your topic", "Calibrate", "Sources"],
  aboutTitle: "What do you cover?",
  aboutHint: "One sentence is enough. We build your radar and fetch today's pauta.",
  aboutPlaceholder: "fintech, Pix and regulation for people who work in banking",
  aboutLabel: "Your topic",
  nichesLabel: "Or pick areas",
  niches: {
    fintech: "Fintech", cripto: "Crypto", regulacao: "Financial regulation", ia: "AI",
    startups: "Startups", tecnologia: "Technology", mercado: "Markets",
    politica: "Politics", carreira: "Careers & HR", geral: "General news",
  },
  next: "Continue",
  creating: "Creating your radar…",
  calibTitle: "Calibrate",
  calibHint: "Real stories from the last 48 h. Say whether they belong in your pauta — each answer teaches the radar.",
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
  emptyDeck: "No fresh stories to calibrate yet — your radar learns as you use it.",
  sourcesTitle: "Sources",
  sourcesHint: "Pre-selected from your areas. Uncheck what you don't want — your own sources live in Settings.",
  removed: "Removed",
  finish: "See today's pauta",
  finishing: "Opening…",
  noSources: "No sources selected.",
  hoursAgo: "{n} h ago",
  errAbout: "Write your topic or pick an area.",
};

export type StartStrings = typeof pt;

export const NICHE_KEYS = [
  "fintech", "cripto", "regulacao", "ia", "startups", "tecnologia", "mercado",
  "politica", "carreira", "geral",
];

export function useStart(): StartStrings {
  const { lang } = useT();
  return lang === "pt" ? pt : en;
}
