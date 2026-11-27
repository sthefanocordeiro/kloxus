import type { Lang } from "../i18n";

// Legal page copy (KLOX-70) lives outside the i18n dict on purpose:
// /termos and /privacidade are lazy routes, so their text must not
// land in the "/" payload (KLOX-16 budget). pt-BR is the original;
// EN ships because the header locale toggle is site-wide.
export const legalDict = {
  en: {
    updated: "Last updated: November 2026",
    contact: "Questions about this page:",
    terms: {
      title: "Terms of Use",
      intro:
        "These Terms of Use govern access to and use of Kloxus, a tool that reads public sources from the catalog, orders the day's news for your Radar and helps you turn your text into versions for LinkedIn, X, HTML and Markdown. By creating a session or using the service, you agree to these terms.",
      sections: [
        {
          h: "The service",
          body: [
            "Kloxus reads public sources from the catalog, organizes the day's news for your Radar and helps you turn your text into versions for LinkedIn, X, HTML and Markdown. AI does not write your text. Nothing leaves Kloxus without an export made by you.",
          ],
        },
        {
          h: "Your session and account",
          body: [
            "You can try Kloxus without signing up: we create a temporary session tied to your browser. To create your account and receive the optional morning email, we ask you to confirm your address through a magic link. Kloxus does not send email to third parties; if enabled, the only email is a daily message to yourself with your Radar news, and you can turn it off at any time. You are responsible for keeping access to your email and for activity that happens under your session.",
          ],
        },
        {
          h: "Your content",
          body: [
            "The text you write is yours. You are the editorially responsible party for what you export and publish, including the use of excerpts and references to the sources you cite.",
            "You grant Kloxus only the license needed to store and process that content in order to operate the service and create your exports.",
          ],
        },
        {
          h: "Third-party sources",
          body: [
            "We read only public sources from the catalog that allow automated reading, and we respect each outlet's rules. Excerpts shown in the product are references for your writing and remain the property of their authors and publishers.",
          ],
        },
        {
          h: "Acceptable use",
          body: [
            "You may not use Kloxus for spam, illegal or misleading content, infringement of third-party rights, or to bypass the rules of the sources. We may limit or end sessions that abuse the service.",
          ],
        },
        {
          h: "Availability and warranties",
          body: [
            "The service is provided “as is”. We may change, suspend or discontinue features; trial sessions may expire and may contain sample data.",
          ],
        },
        {
          h: "Limitation of liability",
          body: [
            "To the extent permitted by law, Kloxus is not liable for indirect damages, for the content of third-party sources, or for what you export and publish.",
          ],
        },
        {
          h: "Changes to these terms",
          body: [
            "We may update these terms. Material changes will be flagged on this page together with the update date.",
          ],
        },
      ],
    },
    privacy: {
      title: "Privacy Policy",
      intro:
        "This Privacy Policy explains what Kloxus collects, how it is used and what your rights are.",
      sections: [
        {
          h: "What we collect",
          body: [
            "Your email address (to create your account and receive the morning email), the content you create, the catalog sources in your Radar, and basic technical session data such as the cookie that keeps you signed in.",
          ],
        },
        {
          h: "How we use it",
          body: [
            "To operate the service: read catalog sources, prepare story lineups, store your content, create your exports and send access links and the morning email. We do not use your data for advertising.",
          ],
        },
        {
          h: "Cookies",
          body: [
            "We use only the session cookie the service needs to work. There are no advertising or third-party analytics trackers.",
          ],
        },
        {
          h: "Sending email",
          body: [
            "Access links and the optional morning email are sent through a transactional email provider. The morning email goes only to your own address, can be turned off at any time and carries an unsubscribe link; unsubscribe requests are honored immediately.",
          ],
        },
        {
          h: "Reading sources",
          body: [
            "We read public pages and feeds from the catalog sources, respecting each site's rules. We do not bypass access restrictions.",
          ],
        },
        {
          h: "Sharing",
          body: [
            "We do not sell personal data. Essential operators (hosting and email delivery) process the minimum needed for the service to run.",
          ],
        },
        {
          h: "Retention",
          body: [
            "No-signup trial sessions may expire. You can request deletion of your data at any time through the contact below.",
          ],
        },
        {
          h: "Your rights",
          body: [
            "Under the LGPD, you may request access, correction, portability or deletion of your personal data.",
          ],
        },
        {
          h: "Changes",
          body: ["We may update this policy; material changes will be flagged on this page."],
        },
      ],
    },
  },
  pt: {
    updated: "Última atualização: novembro de 2026",
    contact: "Dúvidas sobre esta página:",
    terms: {
      title: "Termos de Uso",
      intro:
        "Estes Termos de Uso regem o acesso e o uso do Kloxus, uma ferramenta que lê fontes públicas do catálogo, ordena as notícias do dia para o seu Radar e ajuda você a transformar o seu texto em versões para LinkedIn, X, HTML e Markdown. Ao criar uma sessão ou usar o serviço, você concorda com estes termos.",
      sections: [
        {
          h: "O serviço",
          body: [
            "O Kloxus lê fontes públicas do catálogo, ordena as notícias do dia para o seu Radar e ajuda você a transformar o seu texto em versões para LinkedIn, X, HTML e Markdown. A IA não escreve o seu texto. Nada sai do Kloxus sem uma exportação feita por você.",
          ],
        },
        {
          h: "Sua sessão e conta",
          body: [
            "Você pode experimentar o Kloxus sem cadastro: criamos uma sessão temporária ligada ao seu navegador. Para criar sua conta e receber o e-mail opcional da manhã, pedimos que você confirme seu endereço por um link mágico. O Kloxus não envia e-mails a terceiros; se você ativar, o único e-mail é o diário para você mesmo com as notícias do seu Radar, que pode ser desligado a qualquer momento. Você é responsável por manter o acesso ao seu e-mail e pela atividade feita a partir da sua sessão.",
          ],
        },
        {
          h: "Seu conteúdo",
          body: [
            "O texto que você escreve é seu. Você responde editorialmente pelo que exporta e publica, inclusive pelo uso de trechos e referências das fontes citadas.",
            "Você concede ao Kloxus apenas a licença necessária para armazenar e processar esse conteúdo a fim de operar o serviço e criar suas exportações.",
          ],
        },
        {
          h: "Fontes de terceiros",
          body: [
            "Lemos apenas fontes públicas do catálogo que permitem leitura automatizada e respeitamos as regras de cada veículo. Os trechos exibidos no produto servem de referência para a sua escrita e continuam pertencendo aos respectivos autores e veículos.",
          ],
        },
        {
          h: "Uso aceitável",
          body: [
            "Você não pode usar o Kloxus para spam, conteúdo ilegal ou enganoso, violação de direitos de terceiros ou para contornar as regras das fontes. Podemos limitar ou encerrar sessões que abusem do serviço.",
          ],
        },
        {
          h: "Disponibilidade e garantias",
          body: [
            "O serviço é oferecido “como está”. Podemos alterar, suspender ou descontinuar funcionalidades; sessões de teste podem expirar e conter dados de exemplo.",
          ],
        },
        {
          h: "Limitação de responsabilidade",
          body: [
            "Na medida permitida por lei, o Kloxus não responde por danos indiretos, pelo conteúdo das fontes de terceiros nem pelo que você exporta e publica.",
          ],
        },
        {
          h: "Alterações destes termos",
          body: [
            "Podemos atualizar estes termos. Mudanças relevantes serão indicadas nesta página junto com a data de atualização.",
          ],
        },
      ],
    },
    privacy: {
      title: "Política de Privacidade",
      intro:
        "Esta Política de Privacidade explica o que o Kloxus coleta, como esses dados são usados e quais são os seus direitos.",
      sections: [
        {
          h: "O que coletamos",
          body: [
            "Seu endereço de e-mail (para criar sua conta e receber o e-mail da manhã), o conteúdo que você cria, as fontes do catálogo do seu Radar e dados técnicos básicos da sessão, como o cookie que mantém seu acesso.",
          ],
        },
        {
          h: "Como usamos",
          body: [
            "Para operar o serviço: ler as fontes do catálogo, preparar pautas, guardar seu conteúdo, criar suas exportações e enviar os links de acesso e o e-mail da manhã. Não usamos seus dados para publicidade.",
          ],
        },
        {
          h: "Cookies",
          body: [
            "Usamos apenas o cookie de sessão necessário ao funcionamento. Não há rastreadores de publicidade nem analíticos de terceiros.",
          ],
        },
        {
          h: "Envio de e-mails",
          body: [
            "Os links de acesso e o e-mail opcional da manhã são enviados por um provedor transacional de e-mail. O e-mail da manhã vai apenas para o seu próprio endereço, pode ser desligado a qualquer momento e inclui link de cancelamento; cancelamentos são respeitados imediatamente.",
          ],
        },
        {
          h: "Leitura de fontes",
          body: [
            "Lemos páginas e feeds públicos das fontes do catálogo, respeitando as regras de cada site. Não contornamos restrições de acesso.",
          ],
        },
        {
          h: "Compartilhamento",
          body: [
            "Não vendemos dados pessoais. Operadores essenciais (hospedagem e envio de e-mail) processam o mínimo necessário para o serviço funcionar.",
          ],
        },
        {
          h: "Retenção",
          body: [
            "Sessões de teste sem cadastro podem expirar. Você pode pedir a exclusão dos seus dados a qualquer momento pelo contato abaixo.",
          ],
        },
        {
          h: "Seus direitos",
          body: [
            "Nos termos da LGPD, você pode pedir acesso, correção, portabilidade ou exclusão dos seus dados pessoais.",
          ],
        },
        {
          h: "Alterações",
          body: [
            "Podemos atualizar esta política; mudanças relevantes serão indicadas nesta página.",
          ],
        },
      ],
    },
  },
} as const;

export type LegalDocT = {
  readonly title: string;
  readonly intro: string;
  readonly sections: readonly { readonly h: string; readonly body: readonly string[] }[];
};

export function legalFor(lang: Lang) {
  return legalDict[lang];
}
