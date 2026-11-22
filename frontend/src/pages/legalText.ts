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
        "These Terms of Use govern access to and use of Kloxus, a tool that helps you research, organize and write newsletters. By creating a session or using the service, you agree to these terms.",
      sections: [
        {
          h: "The service",
          body: [
            "Kloxus reads the public sources you choose, organizes story suggestions and keeps track of where each fact came from. Research may be assisted by AI, but the newsletter text is always written and approved by you. No issue is ever sent without your explicit approval.",
          ],
        },
        {
          h: "Your session and account",
          body: [
            "You can try Kloxus without signing up: we create a temporary session tied to your browser. To send real email, we ask you to confirm your address through a magic link. You are responsible for keeping access to your email and for activity that happens under your session.",
          ],
        },
        {
          h: "Your content",
          body: [
            "The text you write is yours. You are the editorially responsible party for what you publish and send, including the use of excerpts and references to the sources you cite.",
            "You grant Kloxus only the license needed to store and transmit that content in order to operate the service.",
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
          h: "Readers who pay",
          body: [
            "You may reserve sections of your newsletter for paying subscribers. Pricing, billing and payouts follow the conditions presented when the feature is configured.",
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
            "To the extent permitted by law, Kloxus is not liable for indirect damages, for the content of third-party sources, or for the messages you approve and send.",
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
            "Your email address (when you confirm it to send messages), the content you create, the sources you choose, and basic technical session data such as the cookie that keeps you signed in.",
          ],
        },
        {
          h: "How we use it",
          body: [
            "To operate the service: read your chosen sources, prepare story lineups, store your content and send the messages you approve. We do not use your data for advertising.",
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
            "Messages are sent through a transactional email provider. Every message carries an unsubscribe link, and unsubscribe requests are honored immediately.",
          ],
        },
        {
          h: "Reading sources",
          body: [
            "We read public pages and feeds from the catalog sources, respecting each site's rules. We do not bypass paywalls or access restrictions.",
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
        "Estes Termos de Uso regem o acesso e o uso do Kloxus, uma ferramenta que ajuda você a pesquisar, organizar e escrever newsletters. Ao criar uma sessão ou usar o serviço, você concorda com estes termos.",
      sections: [
        {
          h: "O serviço",
          body: [
            "O Kloxus lê as fontes públicas que você escolhe, organiza sugestões de pauta e registra de onde veio cada fato. A pesquisa pode contar com apoio de IA, mas o texto da newsletter é sempre escrito e aprovado por você. Nenhuma edição é enviada sem a sua aprovação explícita.",
          ],
        },
        {
          h: "Sua sessão e conta",
          body: [
            "Você pode experimentar o Kloxus sem cadastro: criamos uma sessão temporária ligada ao seu navegador. Para enviar e-mails de verdade, pedimos que você confirme seu endereço por um link mágico. Você é responsável por manter o acesso ao seu e-mail e pela atividade feita a partir da sua sessão.",
          ],
        },
        {
          h: "Seu conteúdo",
          body: [
            "O texto que você escreve é seu. Você responde editorialmente pelo que publica e envia, inclusive pelo uso de trechos e referências das fontes citadas.",
            "Você concede ao Kloxus apenas a licença necessária para armazenar e transmitir esse conteúdo a fim de operar o serviço.",
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
          h: "Leitores que pagam",
          body: [
            "Você pode reservar seções da sua newsletter para assinantes pagos. Valores, cobrança e repasses seguem as condições apresentadas no momento da configuração do recurso.",
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
            "Na medida permitida por lei, o Kloxus não responde por danos indiretos, pelo conteúdo das fontes de terceiros nem pelas mensagens que você aprova e envia.",
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
            "Seu endereço de e-mail (quando você o confirma para enviar mensagens), o conteúdo que você cria, as fontes que escolhe e dados técnicos básicos da sessão, como o cookie que mantém seu acesso.",
          ],
        },
        {
          h: "Como usamos",
          body: [
            "Para operar o serviço: ler as fontes escolhidas, preparar pautas, guardar seu conteúdo e enviar as mensagens que você aprova. Não usamos seus dados para publicidade.",
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
            "As mensagens são enviadas por um provedor transacional de e-mail. Toda mensagem inclui link de cancelamento, e cancelamentos são respeitados imediatamente.",
          ],
        },
        {
          h: "Leitura de fontes",
          body: [
            "Lemos páginas e feeds públicos das fontes do catálogo, respeitando as regras de cada site. Não contornamos paywalls nem restrições de acesso.",
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
