# Sanity check antes do congelamento — 27/11/2026 (manhã)

**Versão:** bundle `index-DIdIcjSh.js`

**Escopo:** só o que quebra premissa, fluxo ou link. Usei a régua de `prompt-kloxus-reta-final-2026-11-25.md`:
- trilha Logística & Automação;
- sem paywall, envio a leitores, aprovação ou checagem;
- vocabulário pauta/brief;
- e-mail da manhã ligado, com descadastro.

**Como testei:**
- Conta `+kloxus-final-en`, Radar novo "Sanity check fintech" (id 1522).
- Fluxo completo em PT, depois celular.
- Links da landing, do app e do e-mail da manhã que você recebeu hoje às 07h11 (conta `+ultimo-teste`).
- README, `docs/submission.md` e GitHub Pages.

---

## Resumo

| Área | Status |
|---|---|
| Fluxo principal (Radar → Match → Hoje → Montar pauta → Escrever → Exportar → Copiar) | ✅ Passa |
| Idioma preservado ao recarregar | ✅ Corrigido |
| E-mail da manhã + opção no menu da conta | ✅ Chegou às 07h11, com descadastro |
| README, `submission.md`, GitHub Pages | ✅ Trilha, licença e postura de IA certas. **Falta o link do vídeo** |
| **Termos de Uso** | ❌ **Descrevem a newsletter antiga, inclusive "leitores que pagam"** |
| **Rodapé do e-mail da manhã** | ❌ "redação assistida por IA" |
| Editor | 🟡 Dois tropeços que fazem perder texto (abaixo) |

---

## 1. Premissas quebradas (corrigir antes de congelar)

### 1.1 Termos de Uso (PT e EN) — P0
A página `/termos` ainda é a do produto de 12/11. É um link do rodapé da landing que um jurado pode abrir. Contradiz a premissa central (sem paywall, sem envio):

| Trecho atual | Problema |
|---|---|
| "uma ferramenta que ajuda você a pesquisar, organizar e escrever **newsletters**" | Posicionamento antigo |
| "registra de onde veio **cada fato**" | Checagem de fatos saiu |
| "o texto da newsletter é sempre escrito e **aprovado** por você. **Nenhuma edição é enviada** sem a sua aprovação explícita" | Aprovação e envio saíram |
| "Para **enviar e-mails de verdade**…" | Não há envio |
| "Você responde pelo que publica e **envia**" | Idem |
| **"Leitores que pagam — Você pode reservar seções da sua newsletter para assinantes pagos"** | **Paywall: a premissa mais explícita quebrada** |
| "pelas **mensagens que você aprova e envia**" | Idem |

A versão EN tem os mesmos trechos ("Readers who pay", "No issue is ever sent…").

**Sugestão de reescrita curta para o serviço:**

> O Kloxus lê fontes públicas do catálogo, ordena as notícias do dia para o seu Radar e ajuda você a transformar o seu texto em versões para LinkedIn, X, HTML e Markdown. A IA não escreve o seu texto. Nada sai do Kloxus sem uma exportação feita por você. Se você ativar, enviamos um e-mail por dia para você mesmo com as notícias do seu Radar, que pode ser desligado a qualquer momento.

Também:
- tirar a seção "Leitores que pagam";
- trocar "aprova e envia" por "exporta".

### 1.2 Política de Privacidade — P1
- "Seu endereço de e-mail (quando você o confirma **para enviar mensagens**)" → "para criar sua conta e receber o e-mail da manhã".
- "enviar as **mensagens que você aprova**" → "enviar os links de acesso e o e-mail da manhã".
- "as fontes **que escolhe**" → "as fontes do catálogo".

### 1.3 E-mail da manhã — P1

| Trecho | Problema | Correção |
|---|---|---|
| Rodapé: "Kloxus · **sua redação assistida por IA**, com fontes e decisão humana." | Contradiz "a IA nunca escreve" e lembra a checagem de fontes | "Kloxus · as notícias do seu setor, prontas toda manhã. A escrita é sua." |
| "Escolha uma e **vire post** no Kloxus" e "**Criar post** com essa notícia →" | Vocabulário: o objeto é **pauta** | "Escolha uma e comece sua pauta" / "Montar pauta com esta notícia →" |
| Texto simples: "as notícias do seu radar **\*\*IA para Bancos\*\***" | Asteriscos de Markdown aparecem crus em leitores de texto simples | Tirar os `**` da versão texto |
| "O link **assina** este navegador na sua conta" | Português estranho | "O link entra na sua conta neste navegador e vale por 72 horas." |

Funciona: o assunto "Suas notícias de hoje estão prontas" chegou às 07h11 na caixa de entrada (não em Promoções), com as 3 notícias com resumo e link para a matéria, o botão "Abrir no Kloxus" e "Não quero mais este e-mail" (`/daily-mail/unsub`; sem token responde 400). Não cliquei no descadastro para não desligar a sua conta de teste.

---

## 2. Quebras de fluxo

| # | Problema | Como reproduzir | Prioridade |
|---|---|---|---|
| 2.1 | **"Escrever" abre a caixa, mas não põe o cursor nela.** Quem clica e já começa a digitar perde tudo: as teclas vão para a página | Editor → "Escrever" numa notícia → digitar sem clicar na caixa | P1 (uma linha de código: `autoFocus`) |
| 2.2 | **"Remover notícia" (✕) apaga a notícia e o texto sem confirmação nem desfazer** | Editor → ✕ numa notícia com texto | P1 (toast "Notícia removida · Desfazer") |
| 2.3 | Depois de remover a 1ª notícia, o **cabeçalho continua mostrando o título antigo** (a API já tem o novo) | Remover a 1ª notícia e olhar o cabeçalho e o título do Exportar | P2 |
| 2.4 | **O link do e-mail cria uma pauta nova a cada clique.** Dois cliques em "Criar post com essa notícia" geraram as pautas 1016 e 1017, iguais | Abrir `/compor?pub=…&noticia=…` duas vezes | P2 (reaproveitar o rascunho com a mesma notícia) |

Também testei (✅):
- "Ir para exportar" logo depois de digitar salva o texto antes de sair.
- Editar depois de exportar marca a versão como desatualizada.
- A lista de Pautas mostra "Editada desde a exportação".
- Copiar → "Copiado".
- No celular (375 px), os botões respondem e não há rolagem lateral.

---

## 3. Links

| Link | Resultado |
|---|---|
| Landing: Como funciona, Perguntas, Termos, Privacidade, `mailto:` | ✅ Abrem. O conteúdo de Termos e Privacidade está no §1 |
| "Ler matéria" nos cards de Hoje | ✅ Matéria original em nova aba (`target=_blank`, `rel=noreferrer`) |
| Links do e-mail para o app (`/claim?…&next=…`) e "Criar post…" (`/compor`) | ✅ Levam ao Hoje e ao editor com a notícia. Não abri os links do seu e-mail, para não gastar seus tokens; testei a rota com o meu Radar |
| Rotas antigas (`/p/…`, `/unsub`, `/publications/:id/outbox`) | ✅ 404 / "Não encontrado" |
| `/issues/:id`, `/runs/:id`, rota inexistente | ✅ Redirecionam ou mostram "Não encontrado" |
| README: app, `submission.md`, docs, `LICENSE.md` | ✅ Licença PolyForm, trilha Logística & Automação, sem MIT, paywall ou Daily Journal |
| README → `kloxus-demo` | 🟡 Citado só na parte de deploy ("fixture fallback"), mas **`https://kloxus-demo.fly.dev` não responde**. Tirar a menção ou dizer que é só para rodar localmente |
| `docs/submission.md` | ❌ **Vídeo: "TBD"**. Preencher antes de submeter (é 1/4 da nota: comunicação) |
| GitHub Pages (`sthefanocordeiro.github.io/kloxus`) | ✅ Atualizado: trilha nova, app funcionando, e-mail das 7h, sem "concept". Também não tem link do vídeo |

---

## 4. Para ficar de olho (não bloqueia)

- **Notícias fora do tema** nos 12 cards de hoje:
  - "Influencer relata erro em diagnóstico de bipolaridade" (g1);
  - "Carne brasileira 'dopada'?";
  - "Intercâmbio após os 40".

  A calibração veio forte (6 de 8 sobre Pix/BC).
- **"Pix passa cartão na largada da 'Black Week'"** é matéria de pagamentos, não promoção. Mas tem "Black Week" no título; se alguém filtrar por essa palavra, vale saber que ela aparece.
- **Landing com sessão ativa** mostra "Entrar / Criar meu Radar" em vez de "Ir para o painel".
- **O limite de sessões por IP (429)** não foi retestado para não derrubar o seu IP de novo. Confirmar com os agentes se a landing ainda cria sessão.

---

## 5. Resíduos

- Conta `+kloxus-final-en`, Radar 1522 "Sanity check fintech":
  - pauta 1015 exportada no LinkedIn e editada depois;
  - pautas 1016 e 1017 em rascunho (duplicadas pelo teste do link do e-mail).
- Nenhum e-mail enviado; nenhum descadastro clicado.
