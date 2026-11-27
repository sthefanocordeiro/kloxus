import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { api } from "./api";
import { hasSessionMarker } from "./lib/session";

// The interface ships in English and Brazilian Portuguese: it follows
// the browser language and offers a switch. Publication content stays
// in the creator's language — only chrome is translated.
export type Lang = "en" | "pt";

export const dict = {
  en: {
    appName: "Kloxus",
    publications: "Radar",
    issues: "Briefs",
    emptyIssues: "No briefs yet — start with today's stories.",
    sources: "Sources",
    issue: "Brief",
    close: "Close",
    dismiss: "Dismiss",
    status: {
      "": "Preparing",
      reviewing: "Decisions",
      drafted: "Draft",
      approved: "Ready",
      scheduled: "Scheduled",
      sent: "Exported",
      exported: "Exported",
      edited: "Edited since export",
      blocked: "Blocked",
    },
    errors: {
      conflict: "The document changed — reloaded the latest version",
      read_only: "This brief was already sent — it is read-only",
      validation: "Something in the request is not valid",
      not_found: "This page doesn't exist or belongs to another account.",
      blocked: "Blocked — fix the open items first",
      bad_json: "The request was not valid JSON",
      email_required: "Confirm your email with the magic link before creating your Radar",
      catalog_only: "Sources are picked from the catalog — pasted URLs are not fetched",
      source_policy: "That source's rules don't allow it here",
      invalid_source: "That source is not valid",
      live_only: "Not available in this session",
      ai_disabled: "Not available in this session",
      invalid_url: "Paste a full http(s) address",
      scrape_unavailable: "Link import isn't configured on this deployment",
      scrape_cap: "Today's link-import limit was reached — try again tomorrow",
      scrape_failed: "The page couldn't be read — check the link or try again later",
      scrape_blocked: "The page gave almost no readable text — it may be sign-in only or too script-heavy",
      not_approved: "Get the brief ready first",
      rate_limited: "Too many tries — wait a bit and try again",
      one_newsletter: "This account already has a Radar — one Radar per account",
      internal: "Internal error — try again in a moment",
      generic: "Something went wrong",
    },
    claim: {
      emailPlaceholder: "you@example.com",
      action: "Send link",
      sending: "Sending…",
      sent: (email: string) => `We sent a link to ${email} — it expires in 15 minutes.`,
      openLink: "Open the link",
      claimed: (email: string) => `saved with ${email}`,
      hint: "Confirm your email to save your work.",
    },
    signin: {
      title: "Sign back in",
      lede: "Enter the email you used for your Radar. We will send you a one-time sign-in link.",
      emailLabel: "Email",
      action: "Send sign-in link",
      sending: "Sending…",
      sent: (email: string) => `If this email has a Kloxus account, we sent a sign-in link to ${email}. It expires in 15 minutes.`,
      openLink: "Open the sign-in link",
      createInstead: "Create a new Radar",
    },
    reader: {
      forPaying: "For paying readers",
      unlockHint: "Subscribe to keep reading this and the paid sections.",
      unlocksSections: "Subscribers read the full brief, including:",
      subscribe: "Subscribe",
      subscribed: "Subscribed",
      viewFree: "Preview as non-subscriber",
      viewFull: "Back to full view",
      unsubscribed: "Unsubscribed",
      unsubHint: "You will no longer receive this Radar.",
      unsubFailed: "This unsubscribe link is not valid.",
      subscribeEmail: "Get new briefs by email",
      emailPlaceholder: "you@example.com",
      subscribeEmailAction: "Send sign-in link",
      subscribing: "Sending…",
      confirmSent: "Check your inbox — confirm the subscription to start receiving briefs.",
      alreadySubscribed: "This address is already subscribed.",
      confirmLink: "Confirm the subscription",
    },
    back: "back",
    demo: {
      title: "Test data",
      reset: "Reset test data",
      resetHint: "Restores this session to fresh data — only your session is affected",
      resetDone: "Test data restored",
      newWs: "New session",
      newHint: "Starts a separate session in this browser",
    },
    landing: {
      navSignIn: "Sign in",
      navDashboard: "Dashboard",
      cta: "Create your Radar",
      starting: "Opening your session…",
      eyebrow: "For people who publish about a sector every day",
      headline: "The research is ready. The writing is yours.",
      subhead:
        "Every morning, the brief lands in your inbox: the stories that matter to your sector, collected overnight, deduplicated and ranked, with a link to every story. You pick the angle and write in your own voice, to publish where your audience is.",
      emailNote:
        "You confirm your email by magic link as you create your Radar.",
      waitlistSupport:
        "No AI writing for you. Just the boring work of finding and checking what happened, done overnight, before you open the editor.",
      alreadyCreator: "Already have an account? Sign in",
      shotAlt: "Kloxus: a story list with ticked boxes and the editor with two linked stories",
      howTitle: "How it works",
      howSteps: [
        {
          title: "Say what you cover",
          body: "Your topics. We pick the sources — and respect each site's rules.",
        },
        {
          title: "Pick today's stories",
          body: "Overnight, Kloxus collects, drops duplicates and ranks what matters — learning from every Yes and No. The brief is in your inbox by 7 a.m.",
        },
        {
          title: "Write and export",
          body: "One text becomes a LinkedIn post, an X thread, HTML and Markdown.",
        },
      ],
      cardsTitle: "Made for people who write",
      cards: [
        { title: "Your voice", body: "AI does the research. The words and the opinion are yours." },
        { title: "Link to the source", body: "Every story links to the original article." },
        { title: "A Radar that keeps watching", body: "Tell Kloxus your topic once; it keeps the lineup coming." },
      ],
      faqTitle: "Questions",
      faq: [
        {
          q: "Do I need an account?",
          a: "Your email is the account — confirm one link and create your Radar. No password.",
        },
        {
          q: "Does the AI write for me?",
          a: "No. It finds, groups and ranks the stories. The writing is yours.",
        },
        {
          q: "Which sources can I use?",
          a: "Sources that allow automated reading. We respect each site's rules.",
        },
        {
          q: "Who chooses the stories?",
          a: "You do, never the AI. Kloxus only suggests.",
        },
        {
          q: "When does the brief arrive?",
          a: "The Radar collects, dedupes and ranks overnight; the assembled brief reaches your email by 7 a.m.",
        },
        {
          q: "Is it for teams or market intelligence?",
          a: "Not yet — market intelligence and team clipping are expansion paths; today the Radar serves one person who publishes about a sector.",
        },
      ],
      closingTitle: "Your next brief starts here.",
      footerProduct: "Product",
      footerLegal: "Legal",
      footerTerms: "Terms",
      footerPrivacy: "Privacy",
      footerContact: "Contact",
      menu: "Menu",
      sourcesStrip: "Works with the feeds you already read",
      mock: {
        railTitle: "Today's stories",
        editorTitle: "Your text",
        editorHint: "Write here, in your own voice…",
        items: [
          "Regulator opens consultation on AI in hiring",
          "New handheld console surprises on battery life",
          "Open Portuguese text corpus launches",
        ],
      },
    },
    create: {
      rejectTerms: "Never show",
      rejectTermsHint: "Themes that never make it in — e.g. phones, games.",
      next: "Next",
    },
    sourcesPage: {
      title: "Radar",
      subtitle: "Where the Radar reads.",
      loading: "Loading sources…",
      generalistHint: "When a beat has no dedicated feed, the Radar reads broad news portals and filters stories by your topics.",
      added: "Your sources",
      offered: "Suggested sources",
      remove: "Remove",
      preview: "See latest",
      lastOk: "working",
      lastFail: "failing",
      confirm: (n: number) => `Add ${n} suggested source${n === 1 ? "" : "s"}`,
      confirming: "Adding…",
      selected: (n: number) => `${n} source${n === 1 ? "" : "s"} selected`,
      empty: "No sources yet — pick one from the catalog.",
      suspendedBadge: "paused",
      paywallBadge: "paywall",
      aderenciaTitle: "Topics",
      aderenciaHint: "Stories rank by these topics.",
      name: "Name",
      newsletterTheme: "Topics",
      themePlaceholder: "e.g. fintech and regulation",
      rejectsLabel: "Never show",
      editorias: "Beats",
      price: "Price",
      pricePlaceholder: "e.g. $10/month",
      save: "Save",
      saving: "Saving…",
      saved: "Saved — applies to next briefs.",
      dangerTitle: "Delete this Radar",
      dangerBody: "Deleting removes the Radar, its briefs and sources. This cannot be undone.",
      deleteAction: "Delete Radar",
      deleteConfirmTitle: "Delete this Radar?",
      deleteConfirmBody: (name: string) => `${name} and everything it owns — briefs and sources — will be deleted. This cannot be undone.`,
      deleteConfirmAction: "Yes, delete it",
      deleting: "Deleting…",
      deleted: (name: string) => `${name} deleted`,
      cancel: "Cancel",
    },
    error: {
      title: "Something broke",
      detail: "The screen hit an unexpected error.",
      retry: "Reload",
      home: "Back to Radar",
      notFoundTitle: "Not found",
      notFoundDetail: "This page doesn't exist or belongs to another account.",
      rateLimitedTitle: "A lot of people are signing in at once from here",
      rateLimitedBody: "Try again in a few minutes.",
      rateLimitedRetry: "Try again",
      rateLimitedWait: (s: number) => `Try again in ${s}s`,
    },
    nav: {
      today: "Today",
      radar: "Radar",
      edition: "Current brief",
      editions: "Briefs",
      export: "Export",
      more: "More",
      back: "Back",
      backTo: (d: string) => `Back to ${d}`,
      account: "Account",
      saveWithEmail: "Save with your email",
      savedWith: (e: string) => `Saved with ${e}`,
      newNewsletter: "New Radar",
      signOut: "Sign out",
      creating: "Create your Radar",
      language: "Language",
      dailyMail: "Receive the day's stories by email",
    },
    steps: {
      of: (n: number, m: number) => `Step ${n} of ${m}`,
    },
  },
  pt: {
    appName: "Kloxus",
    publications: "Radar",
    issues: "Pautas",
    emptyIssues: "Nenhuma pauta ainda — monte a primeira.",
    sources: "Fontes",
    issue: "Pauta",
    close: "Fechar",
    dismiss: "Descartar",
    status: {
      "": "Preparando",
      reviewing: "Em decisões",
      drafted: "Rascunho",
      approved: "Pronta",
      scheduled: "Agendada",
      sent: "Exportada",
      exported: "Exportada",
      edited: "Editada desde a exportação",
      blocked: "Bloqueada",
    },
    errors: {
      conflict: "O documento mudou — a versão mais recente foi carregada",
      read_only: "Esta pauta já foi enviada — é somente leitura",
      validation: "Há algo inválido no pedido",
      not_found: "Esta página não existe ou é de outra conta.",
      blocked: "Bloqueado — resolva as pendências primeiro",
      bad_json: "O pedido não era JSON válido",
      email_required: "Confirme seu e-mail pelo link mágico antes de criar o Radar",
      catalog_only: "As fontes vêm do catálogo — links colados não são coletados",
      source_policy: "As regras dessa fonte não permitem usá-la aqui",
      invalid_source: "Essa fonte não é válida",
      live_only: "Indisponível aqui",
      ai_disabled: "Indisponível aqui",
      invalid_url: "Cole um endereço http(s) completo",
      scrape_unavailable: "A importação por link não está configurada neste ambiente",
      scrape_cap: "O limite diário de importação por link foi atingido — tente amanhã",
      scrape_failed: "Não conseguimos ler a página — confira o link ou tente mais tarde",
      scrape_blocked: "A página quase não deu texto legível — pode exigir login ou depender de scripts",
      not_approved: "Deixe a pauta pronta primeiro",
      rate_limited: "Muitas tentativas — espere um pouco e tente de novo",
      one_newsletter: "Esta conta já tem um Radar — um Radar por conta",
      internal: "Erro interno — tente de novo em instantes",
      generic: "Algo deu errado",
    },
    claim: {
      emailPlaceholder: "você@exemplo.com",
      action: "Enviar link",
      sending: "Enviando…",
      sent: (email: string) => `Enviamos um link para ${email} — ele expira em 15 minutos.`,
      openLink: "Abrir o link",
      claimed: (email: string) => `salvo com ${email}`,
      hint: "Confirme seu e-mail para salvar seu trabalho.",
    },
    signin: {
      title: "Entrar de novo",
      lede: "Digite o e-mail que você usou no seu Radar. Vamos enviar um link de acesso único.",
      emailLabel: "E-mail",
      action: "Enviar link de acesso",
      sending: "Enviando…",
      sent: (email: string) => `Se este e-mail tiver uma conta no Kloxus, enviamos um link de acesso para ${email}. Ele expira em 15 minutos.`,
      openLink: "Abrir o link de acesso",
      createInstead: "Criar um novo Radar",
    },
    reader: {
      forPaying: "Para assinantes",
      unlockHint: "Assine para continuar a ler esta e as seções pagas.",
      unlocksSections: "Assinantes leem a edição inteira, incluindo:",
      subscribe: "Assinar",
      subscribed: "Assinante",
      viewFree: "Ver como não assinante",
      viewFull: "Voltar à visão completa",
      unsubscribed: "Inscrição cancelada",
      unsubHint: "Você não receberá mais este Radar.",
      unsubFailed: "Este link de cancelamento não é válido.",
      subscribeEmail: "Receba pautas novas por e-mail",
      emailPlaceholder: "você@exemplo.com",
      subscribeEmailAction: "Enviar link de acesso",
      subscribing: "Enviando…",
      confirmSent: "Confira sua caixa de entrada — confirme a inscrição para receber as pautas.",
      alreadySubscribed: "Este endereço já está inscrito.",
      confirmLink: "Confirmar a inscrição",
    },
    back: "voltar",
    demo: {
      title: "Dados de teste",
      reset: "Restaurar dados de teste",
      resetHint: "Restaura apenas a sua sessão — não afeta outras pessoas",
      resetDone: "Dados de teste restaurados",
      newWs: "Nova sessão",
      newHint: "Começa uma sessão separada neste navegador",
    },
    landing: {
      navSignIn: "Entrar",
      navDashboard: "Ir para o painel",
      cta: "Criar meu Radar",
      starting: "Abrindo sua sessão…",
      eyebrow: "Para quem publica sobre um setor todo dia",
      headline: "A pauta pronta. A\u00a0escrita é sua.",
      subhead:
        "Toda manhã, a pauta chega por e-mail: as notícias que importam para o seu setor, coletadas de madrugada, sem repetições e já ordenadas, com o link de cada matéria. Você escolhe o ângulo e escreve do seu jeito, na sua voz. Para publicar onde sua audiência está.",
      emailNote:
        "Você confirma seu e-mail por link mágico ao criar o Radar.",
      waitlistSupport:
        "Sem IA escrevendo por você. Só o trabalho chato de achar e organizar o que aconteceu, feito de madrugada, antes de você abrir o editor.",
      alreadyCreator: "Já tenho conta · Entrar",
      shotAlt: "O Kloxus: lista de notícias com caixas marcadas e o editor com duas notícias linkadas",
      howTitle: "Como funciona",
      howSteps: [
        {
          title: "Diga do que você fala",
          body: "Seus temas. As fontes, a gente escolhe — e respeita as regras de cada uma.",
        },
        {
          title: "Escolha as notícias do dia",
          body: "De madrugada, o Kloxus coleta, descarta repetições e ordena o que importa — aprendendo com cada Sim e cada Não. A pauta chega por e-mail às 7h.",
        },
        {
          title: "Escreva e exporte",
          body: "Um texto vira post do LinkedIn, thread do X, HTML e Markdown.",
        },
      ],
      cardsTitle: "Feito para quem escreve",
      cards: [
        { title: "Sua voz", body: "A IA pesquisa. O texto e a opinião são seus." },
        { title: "Link para a fonte", body: "Cada notícia leva à matéria original." },
        { title: "Um Radar que não para", body: "Diga o tema uma vez; o Kloxus mantém a pauta chegando." },
      ],
      faqTitle: "Perguntas",
      faq: [
        {
          q: "Preciso criar conta?",
          a: "Seu e-mail é a conta — você confirma um link e cria o Radar. Sem senha.",
        },
        {
          q: "A IA escreve por mim?",
          a: "Não. Ela encontra, agrupa e ordena as notícias. A escrita é sua.",
        },
        {
          q: "Quais fontes posso usar?",
          a: "Fontes que permitem leitura automatizada. Respeitamos as regras de cada site.",
        },
        {
          q: "Quem escolhe as notícias?",
          a: "Você escolhe, nunca a IA. O Kloxus só sugere.",
        },
        {
          q: "A que horas a pauta chega?",
          a: "O Radar coleta, separa repetições e ordena de madrugada; a pauta montada chega ao seu e-mail às 7h.",
        },
        {
          q: "Serve para equipe ou inteligência de mercado?",
          a: "Ainda não — inteligência de mercado e clipping de equipe são caminhos de expansão; hoje o Radar é para quem publica sobre um setor.",
        },
      ],
      closingTitle: "Sua próxima pauta começa aqui.",
      footerProduct: "Produto",
      footerLegal: "Jurídico",
      footerTerms: "Termos",
      footerPrivacy: "Privacidade",
      footerContact: "Contato",
      menu: "Menu de navegação",
      sourcesStrip: "Funciona com os feeds que você já lê",
      mock: {
        railTitle: "Notícias de hoje",
        editorTitle: "Seu texto",
        editorHint: "Escreva aqui, no seu tom…",
        items: [
          "Regulador abre consulta sobre IA em contratações",
          "Novo console portátil surpreende na bateria",
          "Base aberta de textos em português é lançada",
        ],
      },
    },
    create: {
      rejectTerms: "Nunca mostrar",
      rejectTermsHint: "Temas que nunca entram — ex.: celular, games.",
      next: "Avançar",
    },
    sourcesPage: {
      title: "Radar",
      subtitle: "As fontes que o Radar acompanha.",
      loading: "Carregando fontes…",
      generalistHint: "Quando uma editoria não tem fonte dedicada, o Radar lê portais de cobertura ampla e filtra as notícias pelos seus temas.",
      added: "Suas fontes",
      offered: "Fontes sugeridas",
      remove: "Remover",
      preview: "Ver últimas",
      lastOk: "funcionando",
      lastFail: "com falha",
      confirm: (n: number) => `Adicionar ${n} fonte${n === 1 ? "" : "s"} sugerida${n === 1 ? "" : "s"}`,
      confirming: "Adicionando…",
      selected: (n: number) => `${n} fonte${n === 1 ? "" : "s"} selecionada${n === 1 ? "" : "s"}`,
      empty: "Nenhuma fonte ainda — escolha uma do catálogo.",
      suspendedBadge: "pausada",
      paywallBadge: "para assinantes",
      aderenciaTitle: "Temas",
      aderenciaHint: "As notícias seguem esses temas.",
      name: "Nome",
      newsletterTheme: "Temas",
      themePlaceholder: "ex.: fintech e regulação",
      rejectsLabel: "Nunca mostrar",
      editorias: "Editorias",
      price: "Preço",
      pricePlaceholder: "ex.: R$ 10/mês",
      save: "Salvar",
      saving: "Salvando…",
      saved: "Salvo — vale para a próxima pauta.",
      dangerTitle: "Apagar este Radar",
      dangerBody: "Apagar remove o Radar, suas pautas e fontes. Isso não pode ser desfeito.",
      deleteAction: "Apagar Radar",
      deleteConfirmTitle: "Apagar este Radar?",
      deleteConfirmBody: (name: string) => `${name} e tudo que ela tem — pautas e fontes — será apagado. Isso não pode ser desfeito.`,
      deleteConfirmAction: "Sim, apagar",
      deleting: "Apagando…",
      deleted: (name: string) => `${name} apagado`,
      cancel: "Cancelar",
    },
    error: {
      title: "Algo deu errado",
      detail: "A página encontrou um erro inesperado.",
      retry: "Recarregar",
      home: "Voltar ao Radar",
      notFoundTitle: "Não encontrado",
      notFoundDetail: "Esta página não existe ou é de outra conta.",
      rateLimitedTitle: "Muita gente entrando ao mesmo tempo daqui",
      rateLimitedBody: "Tente de novo em alguns minutos.",
      rateLimitedRetry: "Tentar de novo",
      rateLimitedWait: (s: number) => `Tentar de novo em ${s}s`,
    },
    nav: {
      today: "Hoje",
      radar: "Radar",
      edition: "Pauta atual",
      editions: "Pautas",
      export: "Exportar",
      more: "Mais",
      back: "Voltar",
      backTo: (d: string) => `Voltar para ${d}`,
      account: "Conta",
      saveWithEmail: "Salvar com seu e-mail",
      savedWith: (e: string) => `Salvo com ${e}`,
      newNewsletter: "Novo Radar",
      signOut: "Sair",
      creating: "Crie seu Radar",
      language: "Idioma",
      dailyMail: "Receber as notícias do dia por e-mail",
    },
    steps: {
      of: (n: number, m: number) => `Passo ${n} de ${m}`,
    },
  },
} as const;

export type T = (typeof dict)["en"];

const LangCtx = createContext<{ lang: Lang; t: T; setLang: (l: Lang) => void }>({
  lang: "en",
  t: dict.en,
  setLang: () => {},
});

// KLOX-232: the language choice survives reloads — it is kept in
// localStorage and saved on the account (POST /api/v1/workspace/lang).
// A magic link carries ?lang=, which wins over both.
export const LANG_KEY = "kloxus.lang";

const asLang = (v: string | null | undefined): Lang | null =>
  v === "en" || v === "pt" ? v : null;

const urlLang = (): Lang | null => {
  try {
    return asLang(new URLSearchParams(window.location.search).get("lang"));
  } catch {
    return null;
  }
};

const savedLang = (): Lang | null => {
  try {
    return asLang(localStorage.getItem(LANG_KEY));
  } catch {
    return null;
  }
};

export function LangProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(
    () => urlLang() ?? savedLang() ?? ((navigator.language || "").toLowerCase().startsWith("pt") ? "pt" : "en"),
  );
  // the toggle and the account-seeded value both funnel here; the
  // ?lang= param is consumed once and stripped so a shared URL does not
  // pin the next reader's language.
  const setLang = useCallback((l: Lang) => {
    setLangState(l);
    // the account write only fires for a real session — a landing
    // visitor's toggle must not mint a workspace (KLOX-193)
    if (hasSessionMarker()) void api.saveLang(l).catch(() => {});
  }, []);
  useEffect(() => {
    if (!urlLang()) return;
    const u = new URL(window.location.href);
    u.searchParams.delete("lang");
    window.history.replaceState(null, "", u);
  }, []);
  // <html lang> follows the interface language, not the static
  // index.html attribute (KLOX-56).
  useEffect(() => {
    document.documentElement.lang = lang === "pt" ? "pt-BR" : "en";
    try {
      localStorage.setItem(LANG_KEY, lang);
    } catch {
      // private mode can refuse storage — the in-memory choice still holds
    }
  }, [lang]);
  return <LangCtx.Provider value={{ lang, t: dict[lang] as T, setLang }}>{children}</LangCtx.Provider>;
}

export function useT() {
  return useContext(LangCtx);
}
