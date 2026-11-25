import { useT } from "../i18n";

// Onboarding strings (KLOX-123, reshaped by KLOX-159 / D3-D4: three
// steps — Criar Radar, Dar match, Pronto!). Only /start imports this
// module, so it stays out of the landing chunk (KLOX-16 budget).
const pt = {
  gateTitle: "Confirme seu e-mail para criar o radar",
  gateHint: "Enviamos um link mágico — sem senha.",
  step: ["Criar Radar", "Dar match", "Pronto!"],
  aboutTitle: "Criar seu Radar",
  aboutHint: "Dê um nome e liste os temas. As fontes são escolhidas automaticamente.",
  nameLabel: "Nome do Radar",
  namePlaceholder: "Fintech para bancários",
  aboutLabel: "Temas (separados por vírgula)",
  aboutPlaceholder: "Pix, open finance, Drex, regulação do BC",
  nichesLabel: "Editorias (opcional)",
  niches: {
    politica: "Política", economia: "Economia", "mercado-financeiro": "Mercado financeiro e investimentos",
    "financas-pessoais": "Finanças pessoais", negocios: "Negócios e empresas", startups: "Startups e inovação",
    carreira: "Carreira e trabalho", tecnologia: "Tecnologia", agronegocio: "Agronegócio",
    internacional: "Internacional", "brasil-cidades": "Brasil e cidades", "justica-direito": "Justiça e direito",
    saude: "Saúde", educacao: "Educação", ciencia: "Ciência", "meio-ambiente": "Meio ambiente e sustentabilidade",
    cultura: "Cultura e entretenimento", "estilo-vida": "Estilo de vida e consumo",
  } as Record<string, string>,
  next: "Continuar",
  creating: "Criando seu Radar…",
  calibTitle: "Dar match",
  calibHint: "Notícias reais das últimas 48 h. Diga se esta notícia interessa a você — cada resposta ensina o Radar.",
  loadingDeck: "Buscando notícias reais…",
  loadingSlow: "Isso pode levar alguns segundos — as notícias aparecem aqui assim que chegarem.",
  doneLoading: "Separando as notícias de hoje para você…",
  yes: "Sim",
  no: "Não",
  skip: "Pular",
  keys: "Atalhos: ← não · → sim",
  progress: "{n} de {total}",
  outlets: "{n} veículos",
  hidePrompt: "Esconder {topic}?",
  hideYes: "Esconder",
  hideNo: "Manter",
  hidden: "{topic} escondido das notícias de hoje.",
  emptyDeck: "Ainda não há notícias do seu tema para dar match — o Radar aprende conforme você usa.",
  doneTitle: "Pronto!",
  doneHint: "Seu Radar está no ar e já aprendeu com suas respostas.",
  doneRadar: "Radar",
  doneTopics: "Temas",
  doneMatches: "{n} notícias combinam com seu Radar hoje.",
  doneNone: "Ainda não há notícias do seu tema hoje — o Radar segue buscando.",
  firstPost: "Montar minha primeira pauta",
  opening: "Abrindo…",
  seeToday: "Ver as notícias de hoje",
  hoursAgo: "há {n} h",
  justNow: "agora",
};

const en: typeof pt = {
  gateTitle: "Confirm your email to create your radar",
  gateHint: "We send a magic link — no password.",
  step: ["Create Radar", "Match", "Done!"],
  aboutTitle: "Create your Radar",
  aboutHint: "Give it a name and list the topics. Sources are picked for you.",
  nameLabel: "Radar name",
  namePlaceholder: "Fintech for bankers",
  aboutLabel: "Topics (comma separated)",
  aboutPlaceholder: "Pix, open finance, Drex, central bank rules",
  nichesLabel: "Beats (optional)",
  niches: {
    politica: "Politics", economia: "Economy", "mercado-financeiro": "Financial markets and investing",
    "financas-pessoais": "Personal finance", negocios: "Business and companies", startups: "Startups and innovation",
    carreira: "Career and work", tecnologia: "Technology", agronegocio: "Agribusiness",
    internacional: "International", "brasil-cidades": "Brazil and cities", "justica-direito": "Justice and law",
    saude: "Health", educacao: "Education", ciencia: "Science", "meio-ambiente": "Environment and sustainability",
    cultura: "Culture and entertainment", "estilo-vida": "Lifestyle and consumption",
  } as Record<string, string>,
  next: "Continue",
  creating: "Creating your Radar…",
  calibTitle: "Match",
  calibHint: "Real stories from the last 48 h. Say whether this story interests you — each answer teaches the Radar.",
  loadingDeck: "Fetching real stories…",
  loadingSlow: "This can take a few seconds — stories show up here as soon as they arrive.",
  doneLoading: "Picking today's stories for you…",
  yes: "Yes",
  no: "No",
  skip: "Skip",
  keys: "Shortcuts: ← no · → yes",
  progress: "{n} of {total}",
  outlets: "{n} outlets",
  hidePrompt: "Hide {topic}?",
  hideYes: "Hide",
  hideNo: "Keep",
  hidden: "{topic} hidden from today's stories.",
  emptyDeck: "No stories on your topic to match yet — the Radar learns as you use it.",
  doneTitle: "Done!",
  doneHint: "Your Radar is live and already learned from your answers.",
  doneRadar: "Radar",
  doneTopics: "Topics",
  doneMatches: "{n} stories match your Radar today.",
  doneNone: "No stories on your topic today yet — the Radar keeps looking.",
  firstPost: "Build my first brief",
  opening: "Opening…",
  seeToday: "See today's stories",
  hoursAgo: "{n} h ago",
  justNow: "just now",
};

export type StartStrings = typeof pt;

export const NICHE_KEYS = [
  "politica", "economia", "mercado-financeiro", "financas-pessoais", "negocios", "startups", "carreira",
  "tecnologia", "agronegocio", "internacional", "brasil-cidades", "justica-direito", "saude", "educacao",
  "ciencia", "meio-ambiente", "cultura", "estilo-vida",
];

// The exact pt labels cadastro folds into the topic text — the same
// strings the backend's nicheLabels uses. Sources derives the editoria
// chips a theme still names from these, and rewrites the theme when a
// chip toggles (KLOX-235).
export const NICHE_LABEL_PT: Record<string, string> = {
  politica: "política", economia: "economia", "mercado-financeiro": "mercado financeiro e investimentos",
  "financas-pessoais": "finanças pessoais", negocios: "negócios e empresas", startups: "startups e inovação",
  carreira: "carreira e trabalho", tecnologia: "tecnologia", agronegocio: "agronegócio",
  internacional: "internacional", "brasil-cidades": "Brasil e cidades", "justica-direito": "justiça e direito",
  saude: "saúde", educacao: "educação", ciencia: "ciência", "meio-ambiente": "meio ambiente e sustentabilidade",
  cultura: "cultura e entretenimento", "estilo-vida": "estilo de vida e consumo",
};

export function useStart(): StartStrings {
  const { lang } = useT();
  return lang === "pt" ? pt : en;
}
