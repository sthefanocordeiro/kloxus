# Auditoria Kloxus do zero — 23/11/2026 (noite)

**Versão:** https://kloxus.fly.dev, bundle `index-BzfeVBwW.js` (30 chunks, 1,18 MB de JS)
**Como testei:**
- Navegador sem sessão; e-mail confirmado pelo link mágico enviado ao Gmail do Sthéfano, com autorização dele.
- Radar novo "Fintech para bancários" (id 1499).
- Fluxo completo até a exportação.
- Varredura de todos os textos do bundle.
- Checagem em 375 px.

**Régua:** os pedidos de hoje à noite.
1. Trocar "Peça" por **pauta**.
2. Acabar com o "trem de fatos e fontes".
3. O usuário **escolhe as notícias e já parte para a escrita**.
4. Sumir com seções, guia editorial e demais fluxos antigos.

Também valem as decisões D1–D9 da auditoria da manhã.

---

## 1. Veredito

O começo da jornada melhorou muito; dali para frente o produto volta a ser a newsletter antiga.

**O que funciona bem:**
- O cadastro novo está pronto: **Criar Radar → Dar match → Pronto!**, com nome, temas, editorias opcionais e contagem "12 notícias combinam com seu Radar hoje".
- A calibração trouxe notícias do tema (BC, CVM/BRB, juros, capital dos bancos).

**O que vem depois:** ao clicar em **"Criar primeiro post"**, caio direto num editor com:
- "Peça 1";
- "Fontes e fatos";
- frases sublinhadas de vermelho ("a fonte [1] não confirma");
- "Marcar como interpretação";
- "0/2 fatos com fonte";
- "Ver pendências";
- uma etapa "Pautas" com **50 vagas vazias** ("Solte uma pauta aqui · 50 vagas").

Ou seja: o motor de checagem de fatos e o modelo de seções continuam inteiros por baixo. Só a porta de entrada mudou.

**Rotas antigas ainda abertas, com textos de newsletter:**
- `/sections`: modelos "Clássica / Resumo curto / Ensaio + links" e comentário obrigatório.
- `/template`: "Modelo do Radar".
- `/guide`: "Guia editorial".
- `/readers`: "0 leitores · 0 assinantes".
- `/outbox`: "Caixa de saída".

As três primeiras aparecem como abas da tela **Radar**.

**"Peça" aparece 59 vezes no código.**

Há um bug que explica a pauta fraca. O Radar novo ficou com **só 2 fontes ativas** (Tecnoblog e Valor). As outras 20 sugeridas vêm marcadas, mas só entram se o usuário achar o botão "Confirmar 20 fontes e coletar". Resultado: **as 12 notícias da pauta vêm do Valor Econômico**, que tem paywall.

---

## 2. A jornada, passo a passo (o que vi)

| # | Passo | O que aconteceu | Nota |
|---|---|---|---|
| 1 | Landing → "Criar meu Radar" | Botão funciona. Mockup ainda mostra "aprovada", "O destaque", "SEU COMENTÁRIO — A IA NUNCA ESCREVE AQUI", "[1]". Texto fala em "fonte e fatos checáveis" e "Fontes à vista — cada fato mostra de onde veio" | ❌ texto e mockup antigos |
| 2 | "Confirme seu e-mail para criar o radar" | O cadastro agora **exige e-mail antes de tudo**. O botão diz "Salvar com seu e-mail" (texto de outra tela). O FAQ diz "Preciso criar conta? Não para testar", o que contradiz a tela | 🟡 |
| 3 | E-mail do link mágico | Assunto "Salve o seu Kloxus com este e-mail" e botão "Guardar o meu Kloxus" não falam em Radar. O 1º e-mail foi para **Promoções** | 🟡 |
| 4 | Página `/claim` | **HTML sem estilo**: fonte do sistema e botão cinza padrão do navegador. Depois de "Confirmar", **volta para a landing**, e é preciso clicar "Criar meu Radar" de novo | ❌ |
| 5 | Criar seu Radar (1/3) | Nome, temas por vírgula e editorias opcionais ✅. Os chips são de nicho financeiro (Economia, Negócios, Carreira, Tecnologia, Mercado, Fintech, Cripto, Regulação financeira, IA, Startups), não as 18 editorias do levantamento. O chip selecionado quase não se distingue | 🟡 |
| 6 | Dar match (2/3) | ~14 s em "Buscando notícias reais…", depois 8 cards. **5 de 8 no tema** (CVM/BRB, dólar/BC, Durigan, nomes para o BC, capital dos bancos); 3 fora (Anatel, IA no varejo, BID/arte). Todos do Valor. Sem resumo nos cards | ✅ grande melhora |
| 7 | Pronto! (3/3) | Nome, temas e "12 notícias combinam com seu Radar hoje" ✅. Por ~5 s aparece "…" e o botão fica desabilitado sem indicar que está carregando. O cabeçalho continua "Crie seu Radar" | ✅ |
| 8 | "Criar primeiro post" | **Pula a escolha das notícias**: abre "Peça 1" já no passo 2 ("Escrever"), só com a 1ª notícia em que eu disse Sim | ❌ |
| 9 | Etapa "Pautas" do editor | "Nenhuma sugestão nova desde a última peça" num Radar recém-criado com 12 notícias. "Escrever eu mesmo / Redigir a peça (0 pautas)". "Suas mudanças de seções valem para a próxima peça". Seção "NOTÍCIAS 0/50" com **50 vagas** e atalhos "1-3 escolhe a seção" | ❌ fluxo antigo inteiro |
| 10 | Hoje → "Escrever" em outro card | **Adiciona** a notícia à peça aberta e leva ao editor. Funciona, mas para montar 3 notícias é preciso ir e voltar 3 vezes. Não há seleção múltipla | 🟡 |
| 11 | Escrever | Cada notícia tem "Escrever esta pauta", caixa "Nada escrito ainda — digite ou cole o texto e **cite as fontes usadas**", chip "[1] Valor Econômico", "minha leitura — sem fonte", "Adicionar à pauta". O texto é **quebrado em frases** e cada frase recebe "a fonte [1] não confirma" | ❌ é o "trem" |
| 12 | Concluir revisão | Popover "Antes de aprovar": "Todos os fatos com fonte ✓" **e ao mesmo tempo** "2 pendências de fonte por resolver" e "1 pauta sem texto" | ❌ contraditório |
| 13 | Marcar como interpretação | O texto vira `> _Interpretação: …_` com os símbolos à mostra. Clicando de novo **embrulha duas vezes**: `> _Interpretação: > _Interpretação: …__` | ❌ bug |
| 14 | Concluir → "Pronta para aprovação" → **outro** "Concluir revisão" | Dois cliques para a mesma coisa. Status vira "Aprovada #1" | 🟡 |
| 15 | Para onde vai? | Os 4 formatos aparecem. O texto exportado começa com **"Fintech para bancários — peça 1"**, leva o `> _Interpretação…` cru e **não traz o título das notícias nem link** no LinkedIn e no X. No HTML e no Markdown o título aparece, mas sem link | ❌ D8 não feito |

---

## 3. Glossário: o que trocar

Pedido de hoje: **"Peça" → "pauta"**. Isso cria um conflito: "pauta" já é usada hoje para **cada notícia** ("Escreva esta pauta", "Pauta 1 de 2", "Solte uma pauta aqui", "Adicionar à pauta") e para a lista do dia ("Pauta de hoje"). Se "Peça" também virar "pauta", a mesma palavra terá três sentidos na mesma tela.

**Proposta: cada palavra com um sentido só.**

| Conceito | Hoje aparece como | Passa a ser |
|---|---|---|
| A lista de sugestões do dia | Pauta de hoje / Sugestões | **Notícias de hoje** |
| Cada item da lista | pauta, história, sugestão | **notícia** |
| O que o usuário monta e escreve (antes "Peça") | Peça, Peça 1, Peças, "esta peça" | **Pauta** (lista no menu: **Pautas**) |
| O texto que sai para cada destino | versão, rendição, cópia | **post** (Post LinkedIn, Post X, Post de blog/newsletter, Markdown) |
| Status | pauta / rascunho / Revisada / aprovada / Aprovada #1 / agendada / enviada / bloqueada | **Rascunho → Pronta → Exportada** |

Isso respeita o pedido (Peça → pauta). O "post" da manhã (D1) passa a nomear só o que sai em cada formato, que é como o criador já fala.

> **Decisão para o Sthéfano:** se preferir "post" para a coisa que se escreve (como na manhã), basta trocar a linha 3. O que não pode é "pauta" significar notícia e texto ao mesmo tempo.

### Textos a trocar ou apagar (achados nesta auditoria)

| Onde | Texto atual | O que fazer |
|---|---|---|
| Menu lateral | Peças | **Pautas** |
| Cabeçalho do editor | Peça 1 | Título da 1ª notícia ou "Pauta de 23/11" |
| Hoje | "Sim e Não ensinam o radar; Escrever abre uma peça." | "Marque as notícias que entram e clique em Montar pauta." |
| Hoje, rodapé | "Peças em andamento · Peça 1 · Rascunho" | "Pautas em andamento" |
| Editor, etapa 1 | "Nenhuma sugestão nova desde a última peça — adicione fontes ou volte amanhã." | Apagar a etapa (§4) |
| Editor | "Escrever eu mesmo" / "Redigir a peça (0 pautas)" / "Preparando vagas vazias…" | Apagar |
| Editor | "Suas mudanças de seções valem para a próxima peça — esta mantém as dela." | Apagar |
| Editor | "Solte uma pauta aqui · 50 vagas" / "50 vagas em aberto em Notícias" / "J/K navega · 1-3 escolhe a seção" | Apagar |
| Editor | "Escreva esta pauta" / "Adicionar à pauta" | "Escreva sobre esta notícia" (campo único que salva sozinho; sem botão) |
| Editor | "Nada escrito ainda — digite ou cole o texto e cite as fontes usadas." | "Seu texto sobre esta notícia." |
| Editor | "[1] Valor Econômico" (chip) / "minha leitura — sem fonte" | Apagar. A fonte é o link do título |
| Editor | "a fonte [1] não confirma" / "muito próxima da fonte" / "Remover as 2 frases que precisam de fonte" | Apagar |
| Editor | "Marcar como interpretação" / "SUA INTERPRETAÇÃO" / "> _Interpretação: …_" | Apagar (e corrigir o bug do duplo embrulho, se a função ficar em algum lugar) |
| Editor | "0/2 fatos com fonte" / "ainda sem frases" / "Ver pendências" | Apagar. No lugar: "2 notícias · 1 com texto" |
| Painel lateral | Abas "Pautas · Fontes e fatos · Prévia"; "Selecione uma pauta para ver a pesquisa e as fontes." | Deixar só **Prévia**. A notícia original abre pelo título |
| Revisão | "Antes de aprovar" / "Todos os fatos com fonte" / "Nenhuma pendência de fonte" / "Pronta para aprovação" | Apagar. Botão único **"Ir para exportar"** |
| Status | "Aprovada #1" | "Pronta" |
| Exportar | "Esta peça tem 2 histórias. Posts sociais funcionam melhor com uma principal…" | "Esta pauta tem 2 notícias…" |
| Exportar | "Fintech para bancários — peça 1" no início do texto | Apagar o título automático |
| Radar → abas | "Fontes · Seções · Guia editorial" | Só **Fontes** (e o tema) |
| Radar | "As fontes que o Radar acompanha — confirme para começar a primeira coleta." | Apagar. As fontes já entram ativas (P0-2) |
| Radar | "ADERÊNCIA — O QUE AS SUGESTÕES SEGUEM" / "Temas rejeitados ficam na reserva, marcados — nunca entram sozinhos." | "Temas do Radar" |
| Radar | "Pauta · o que entra aqui / qualquer pauta" (sobra das seções) | Apagar |
| Radar | "Temas que ficam fora das peças — ex.: celular, games." | "Temas que nunca entram — ex.: celular, games." |
| Configurações | Campos PÚBLICO, LINHA EDITORIAL, TOM (valor `neutral`, em inglês) e link "Guia editorial" | Apagar. Ficam só Nome, Temas, Editorias e "Apagar Radar" |
| Configurações | "Apagar remove o Radar, suas peças e fontes." | "…suas pautas e fontes." |
| Landing, subtítulo | "…chegam organizadas, com fonte e fatos checáveis." | "…chegam organizadas, com o link de cada matéria." |
| Landing, card | "Fontes à vista — Cada fato mostra de onde veio." | "Link para a fonte — cada notícia leva à matéria original." |
| Landing, passo 3 | "Escreva e revise — Com as fontes ao lado, no seu tom." | "Escreva e exporte — para LinkedIn, X, blog ou newsletter." |
| Landing, FAQ | "Ela lê as fontes, resume e organiza os fatos." / "A IA decide o que entra na minha peça? … toda peça espera sua revisão." | "Ela encontra e resume as notícias." / "…na minha pauta? Nunca. Você escolhe." |
| Landing | "Sua próxima peça começa aqui." | "Sua próxima pauta começa aqui." |
| Landing, mockup | "aprovada", "O destaque", "SEU COMENTÁRIO — A IA NUNCA ESCREVE AQUI", "[1]" | Novo mockup: lista com caixas marcadas → editor com 2 notícias e títulos linkados |
| Landing vs. cadastro | "Preciso criar conta? Não para testar." × cadastro que exige e-mail | Escolher um. Recomendo deixar testar sem e-mail e pedir só para salvar |
| E-mail | "Salve o seu Kloxus com este e-mail" / "Guardar o meu Kloxus" | "Confirme seu e-mail para criar seu Radar" / "Criar meu Radar" |
| Tela do e-mail | Botão "Salvar com seu e-mail" | "Enviar link" |

### Textos que continuam no código, sem tela nova que os use (apagar)

- **Envio/newsletter:** "Enviar aos leitores", "Aprovar e enviar", "Enviar teste", "Enviar agora", "Agendar para depois", "Teste enviado para a caixa de saída", "Resend falhou — a mensagem está na caixa de saída".
- **Assinatura:** "Receba peças novas por e-mail", "Enviar link de acesso", "Você não receberá mais este Radar" (descadastro), "Para assinantes", "Assine para continuar a ler…", "Ver como não assinante", página pública `PublicNewsletterPage`, `/p/:slug`, `/unsub`.
- **Seções e modelo:** "Comece de um modelo", "Clássica / Resumo curto / Ensaio + links", "Seu comentário — obrigatório / opcional / sem comentário", "A seção está cheia — troque uma pauta primeiro", "Modelo do Radar", "Salvar modelo".
- **Guia:** "Guia editorial", "Dê um motivo à rejeição — ele alimenta o guia editorial", "Reescrever a partir das decisões".
- **Execução antiga:** "Preparar próxima peça", "agendada", "enviada", "bloqueada".
- **Checagem:** "Afirmação com número, data ou citação não pode ser interpretação", "A frase não mudou — corrija a afirmação…".

---

## 4. Fluxos antigos ainda vivos

| Fluxo | Onde aparece | O que fazer |
|---|---|---|
| **Seções** (vagas, formatos Destaque/Notícias/Links, comentário obrigatório, capacidade) | Aba "Seções" do Radar (`/sections`); etapa "Pautas" do editor; seletor "Todas as seções" | **Remover** tela, etapa e modelo de dados do fluxo do usuário |
| **Modelo do Radar** (blocos Texto/Título/Endereço/Divisor/Imagem/Incorporar/Pauta/Mensagem) | `/template` | **Remover** |
| **Guia editorial** | Aba do Radar, link em Configurações (`/guide`) | **Remover** |
| **Leitores, assinantes, caixa de saída** | `/readers` mostra "0 leitores confirmados · 0 assinantes · Ver a caixa de saída"; `/outbox` mostra "Caixa de saída" | **Remover** rotas e telas (hoje só não têm link) |
| **Wizard antigo "Seu Radar · Seções · Fontes — Passo 3 de 3"** com Voltar/Avançar | Aparece por ~10 s ao abrir a tela Radar, antes de carregar a versão nova | **Remover** |
| **Checagem de fatos** (frases, citações, pendências, interpretação, "Fontes e fatos") | Todo o passo "Escrever" e a revisão | **Remover do fluxo.** O texto é livre, uma caixa por notícia |
| **Revisão em dois cliques** ("Concluir revisão" → popover → "Concluir revisão") | Editor | Um botão: **"Ir para exportar"** |
| **Campos de perfil editorial** (Público, Linha editorial, Tom) | Configurações | **Remover** |

---

## 5. Problemas por prioridade

### P0 — impedem o fluxo pedido

**P0-1. Escolher as notícias e já escrever: ainda não existe**

Hoje o caminho é:
1. "Criar primeiro post" pula a escolha.
2. A etapa "Pautas" mostra 50 vagas e "nenhuma sugestão".
3. Em Hoje, só se adiciona uma notícia por vez, e cada clique tira o usuário da lista.

**Fluxo pedido:**

**1. Notícias de hoje (lista)**
- Cada card tem uma caixa de seleção; Sim/Não continuam para treinar.
- Barra fixa embaixo: "**3 notícias selecionadas · Montar pauta →**".
- A ordem de seleção vira a ordem do texto (mostrar 1, 2, 3 no card).
- "Criar primeiro post", na tela Pronto!, leva para cá, não para o editor.

**2. Escrever** (uma página)
- As notícias na ordem escolhida, reordenáveis (↑↓ ou arrastar), com botão "remover".
- Cada notícia mostra:
  - o **título linkado** à matéria original (abre em nova aba);
  - fonte e data em cinza;
  - **uma caixa de texto livre** que salva sozinha. Sem quebrar em frases, sem citação, sem sinalização.
- Um campo opcional "Abertura" no topo (o comentário geral), também livre.
- "+ Adicionar notícia" abre a lista de hoje num painel.

**3. Exportar** ("Para onde vai?")
- Um botão "Ir para exportar". Sem checagem. Só avisa se alguma notícia ficou sem texto e deixa seguir.

**P0-2. O Radar novo nasce com 2 fontes, e a pauta vem toda de um veículo com paywall**
- Em "Suas fontes": Tecnoblog e Valor Econômico.
- As 20 sugeridas (InfoMoney, Seu Dinheiro, Money Times, Finsiders, Agência Brasil Economia, Poder360, g1, CNN…) vêm **marcadas, mas inativas** até o usuário clicar "Confirmar 20 fontes e coletar". O botão fica no fim de uma tela que o cadastro novo nem mostra.
- Resultado: 12/12 notícias do **Valor Econômico**, que tem paywall, então o usuário não consegue ler a fonte. Nenhum card tem mais de um veículo.
- **Correção:**
  - as fontes escolhidas pelos temas e editorias entram **ativas** ao criar o Radar;
  - limite de no máximo 3 notícias do mesmo veículo entre as 10 primeiras;
  - veículo com paywall só entra se o usuário marcar.

**P0-3. Exportação com texto quebrado** (D8 continua aberto)
- LinkedIn e X começam com "Fintech para bancários — peça 1" e **não têm título nem link de nenhuma notícia**.
- O texto marcado como interpretação sai com os símbolos crus (`> _Interpretação: …_`), e duplicado.
- No HTML e no Markdown o título entra como `<h3>`/`###` **sem link**.
- **Correção (tabela da manhã):**

| Formato | Saída |
|---|---|
| HTML | `<h3><a href>Título</a></h3>` + texto |
| Markdown | `### [Título](url)` + texto |
| LinkedIn | Título, URL na linha de baixo, texto |
| X | Título + URL (conta 23 caracteres) + texto, dividido por notícia |

### P1 — confundem ou quebram a confiança

1. **Rotas antigas abertas** (`/sections`, `/template`, `/guide`, `/readers`, `/outbox`) e abas "Seções" e "Guia editorial" na tela Radar (§4).
2. **"Peça" em 59 lugares** e "pauta" com três sentidos (§3).
3. **Confirmação de e-mail:**
   - a página `/claim` não tem estilo;
   - depois de confirmar, o usuário volta à landing em vez de seguir para "Criar seu Radar";
   - o e-mail não fala em Radar e já caiu em Promoções;
   - o FAQ diz que não precisa de conta.
4. **Revisão contraditória:** "Todos os fatos com fonte ✓" junto de "0/2 fatos com fonte" e "2 pendências". Some quando o P0-1 for feito, mas hoje é a tela que o usuário vê.
5. **Status com 4 nomes para a mesma pauta:** Hoje diz "Revisada", Pautas diz "aprovada", o editor diz "Aprovada #1", o código ainda tem "agendada", "enviada", "bloqueada".
6. **Configurações** com Público, Linha editorial e Tom (valor `neutral` em inglês). As editorias escolhidas no cadastro viraram texto dentro de "Temas" ("…bancos digitais, economia, regulação financeira") em vez de chips separados.
7. **Landing:** subtítulo, card "Fontes à vista", passo 3, FAQ e mockup ainda vendem "fatos checáveis" e a newsletter antiga.

### P2 — acabamento

- **Chips do cadastro:** são de nicho financeiro. Usar as 18 editorias do levantamento (`editorias-topicos-2026-11-23.md`). O estado selecionado precisa de contraste maior.
- **Dar match:** 14 s de espera só com "Buscando notícias reais…". Mostrar os cards conforme chegam, ou uma barra de progresso.
- **Pronto!:** "…" e botão desabilitado por ~5 s sem explicação. O cabeçalho deveria dizer "Pronto", não "Crie seu Radar".
- **Cards de Hoje e da calibração sem resumo** (antes tinham uma linha); o resumo ajuda a decidir.
- **Tela Radar:** leva ~15 s para carregar, e o wizard antigo pisca nesse meio-tempo.
- **Menu da conta:** só mostra "Salvo com sthefanocordeiro@gmail.com". Falta "Sair" e "Novo Radar" (este só aparece na lateral durante o cadastro).
- **Sidebar:** o nome do Radar leva à lista de pautas. O botão "← Voltar" aparece em telas de topo (Hoje, Radar, Configurações).
- **Mobile 375 px:** sem rolagem lateral ✅. No editor, os nomes das etapas concluídas somem ("· · 3 Revisar").
- **Landing logada:** com sessão ativa, o botão continua "Criar meu Radar" e leva ao Radar existente. Trocar por "Ir para o painel", que já existe no menu do celular.
- **Menu hambúrguer do celular:** agora funciona ✅ (P0-3 da manhã resolvido).

---

## 6. O que melhorou desde a manhã

- Cadastro em 3 passos com contador, nome do Radar separado dos temas, editorias opcionais e tela **Pronto!** (D3, D4) ✅
- Calibração no tema: 5 de 8 ✅ (antes 0 de 8)
- Comentário obrigatório por seção sumiu do editor (D6) ✅. Ainda existe nas telas de Seções.
- CTA da landing leva ao cadastro (a lista de espera saiu) ✅
- Menu hambúrguer do celular funciona ✅
- Sem rolagem lateral no celular ✅

---

## 7. Ordem de trabalho para os agentes

1. **Lista com seleção + "Montar pauta"** → editor de texto livre por notícia → "Ir para exportar" (P0-1).
   - Remover do fluxo: frases, citações, "Fontes e fatos", interpretação, pendências e a etapa "Pautas" com vagas.
2. **Fontes ativas ao criar o Radar** e no máximo 3 por veículo no top 10 (P0-2).
3. **Exportação com título linkado**, sem título automático e sem markdown cru (P0-3).
4. **Glossário único** (§3): Peça → Pauta, notícia para os itens, post para o que sai. Varrer os 59 "peça" e os textos da tabela.
5. **Remover rotas e abas antigas:** Seções, Modelo, Guia editorial, Leitores, Caixa de saída, wizard antigo, campos Público/Linha editorial/Tom (§4).
6. **Confirmação de e-mail:** página `/claim` com o visual do app, seguir direto para "Criar seu Radar", e-mail falando em Radar, FAQ coerente.
7. **Landing:** subtítulo, cards, passo 3, FAQ e mockup novos.

**Critério de aceite do fluxo inteiro** (para o vídeo), num navegador sem sessão:
1. Criar Radar.
2. Match.
3. Pronto!
4. Marcar 3 notícias.
5. Montar pauta.
6. Escrever 2 linhas em cada.
7. Ir para exportar.
8. Post LinkedIn com os 3 títulos e links.
9. Copiar.

Em **menos de 3 minutos**, sem nenhuma das palavras: peça, seção, vaga, fato, interpretação, pendência, aprovada, guia editorial.

---

## 8. Resíduos deste teste

- **Workspace** associado a **sthefanocordeiro@gmail.com** (link mágico confirmado com autorização).
- **2 e-mails "Salve o seu Kloxus"** no Gmail; o 1º já estava na lixeira.
- **Radar 1499 "Fintech para bancários"**, com 8 respostas de calibração e o tema/editorias acima.
- **Pauta 997 ("Peça 1")**, status Aprovada:
  - 2 notícias (CVM/BRB e juros futuros) com textos de teste meus;
  - uma interpretação com o bug do duplo embrulho;
  - 4 versões exportadas (LinkedIn, X, HTML, Markdown).
- **Não confirmei** as 20 fontes sugeridas, para registrar o estado padrão.
