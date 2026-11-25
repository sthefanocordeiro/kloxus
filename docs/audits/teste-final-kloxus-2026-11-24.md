# Teste final Kloxus antes do congelamento — 24/11/2026 (noite)

**Versão:** bundle `index-D8UeqrDP.js`

**O que fiz:**
- Rodada completa em **português**, sem sessão, com e-mail novo (`+kloxus-final-pt`).
- Radar "Fintech para bancários" (id 1502), o caso real do produto.
- Conferi os 3 P0 do teste das 13h.
- Versão em **inglês**: §4.
- Por fim, sugestões de copy, legendas, heurísticas e UX/UI.

---

## 1. Veredito

**O fluxo principal em português está pronto para o vídeo:** Criar Radar → Dar match → Pronto! → marcar notícias → Montar pauta → Escrever → Ir para exportar → Copiar.

- Os três P0 do teste anterior foram resolvidos.
- A pauta de fintech veio boa: Finsiders, Agência Brasil Economia, InfoMoney.
- A thread do X saiu certa.
- O status "Exportada" funciona.

**Um P0 novo antes de congelar:** o **limite de sessões por IP bloqueia o site inteiro**.
- Depois de algumas sessões (logout + novos cadastros), **todas as páginas**, inclusive a landing e `/termos`, passaram a responder com o JSON cru `{"error":{"code":"rate_limited","message":"too many new sessions from this address — try again later"}}` (HTTP 429, sem `Retry-After`).
- Quem divide IP fica de fora: escritório, coworking, banca do hackathon, celular com CGNAT (a maioria das operadoras no Brasil). Na prática, o primeiro grupo que testar junto derruba o site para os outros.
- **O seu próprio navegador está nesse IP** e deve estar bloqueado agora.

---

## 2. Conferência dos P0 do teste das 13h

| P0 anterior | Agora |
|---|---|
| Lista "Pautas" abria o editor antigo | ✅ Abre `/pauta/:id`; `/issues/:id` redireciona para `/pauta/:id/export`; `/runs/:id` dá "Não encontrado" |
| Botões principais cobertos no celular | ✅ "Montar pauta" e "Ir para exportar" ficam acima da barra inferior e respondem ao toque (375 px) |
| Texto perdia o ponto entre frases | ✅ "Teste de persistência. Segunda frase aqui." volta igual e sai igual nos 4 formatos. Agora é um campo de texto por notícia (`PUT /issues/:id/pauta`) |
| Thread do X com `---` | ✅ 1 post para a abertura e 1 por notícia, "n/4", sem `---` |
| "Não" não tirava a notícia de Hoje | ✅ As 4 com "Não" sumiram |
| Status não virava "Exportada" | ✅ Após Copiar: "Exportada · LinkedIn" na lista, com título da 1ª notícia e data |
| Textos antigos | ✅ "editar não reabre a revisão" saiu; "Nunca mostrar"; Radar sem "Pauta · o que entra aqui"; Pronto! diz "Montar minha primeira pauta" |
| Datas ISO | ✅ "há 10 h" e "23/11" |
| ↑ na 1ª e ↓ na última | ✅ Desabilitados |
| Menu lateral antes do Radar existir | ✅ Some |

---

## 3. Rodada em português — o que vi

| Passo | Resultado | Nota |
|---|---|---|
| Landing | Nova, coerente | 🟡 copy (§5) |
| E-mail | Chegou em segundos. **Todos os e-mails de confirmação caem na mesma conversa do Gmail** (mesmo assunto), então o usuário pode abrir um link velho já usado | 🟡 |
| `/claim` | "Confirmar e continuar" → "Criar seu Radar" | ✅ |
| Criar Radar | Nome, temas, 18 editorias | ✅ |
| Dar match | Abriu em ~1 s. Os 8 cards trouxeram regulação (BC/Foxbit/Binance, BaaS), Open Finance, câmbio, crédito e um evento de fintechs. **Depois de 2 "Não", apareceu "Esconder outro?"**: "outro" é a etiqueta interna de tópico vazando para a tela | ✅ / 🟡 |
| Pronto! | "12 notícias combinam com seu Radar hoje" → "Montar minha primeira pauta" | ✅ |
| Notícias de hoje | Cerca de 8 de 12 no tema. Fora do tema: "Prefeitura de Taubaté troca digital por reconhecimento facial", "Jensen Huang retorna à Nvidia", "Bolsas de NY fecham sem direção única". Fontes variadas (Finsiders, Agência Brasil, InfoMoney, Seu Dinheiro, Money Times, g1). Poucos cards têm resumo | ✅ / 🟡 |
| Montar pauta | **8,5 s em "Montando…"** sem dizer o que está acontecendo | 🟡 |
| Escrever | Abertura, títulos linkados, "Comentar" abre a caixa, ↑ ↓ ✕, "Salvo". Emoji e quebra de linha preservados | ✅ |
| Sair do editor pelo menu ou recarregar logo depois de digitar | **Perde o texto** se sair durante o "Salvando…" (~2 s depois da última tecla). "Ir para exportar" salva antes de sair ✅, mas o menu lateral, o "Voltar" e o recarregar não salvam e não avisam | ❌ P1 |
| Exportar | Título linkado em todos os formatos, contador, "ver mais", thread, "Baixar .md". Copiar → "Copiado" → status "Exportada" | ✅ |
| Copiar sem foco | Com a aba sem foco, o primeiro "Copiar" não copiou e **não avisou** | 🟡 |
| Celular 375 px | Sem rolagem lateral; botões tocáveis | ✅ |
| Rotas antigas | `/sections`, `/readers`, `/runs/1` → "Não encontrado" | ✅ |

---

## 4. Versão em inglês

**Limite:** não consegui rodar a versão em inglês pela tela. Quando saí da conta para começar a rodada em EN, o site passou a responder 429 para o meu IP (o P0 do §1). Revisei **todos os textos em inglês do bundle** e comparei com o PT, tela por tela. A rodada EN pela tela fica para quando o bloqueio cair (§7).

**O que está bom:**
- Landing ("The research is ready. The writing is yours.").
- Cadastro ("Create your Radar", "Match", "Done!", "Build my first brief").
- Editor ("Write", "Opening (optional)", "Go to export").
- Exportação ("Where is it going?", "LinkedIn post", "X post", "Suggested thread").
- Erros traduzidos.
- As 18 editorias (Politics, Economy, Financial markets…).

**Corrigir antes de congelar:**

| Onde | Texto atual (EN) | Sugestão |
|---|---|---|
| Hoje, botão | **Build pauta →** | Build brief → |
| Hoje, rodapé | **Pautas in progress** | Briefs in progress |
| Hoje, vazio | **No pautas yet.** Tick stories and click **Build pauta**. | No briefs yet. Tick stories and click Build brief. |
| Lista | **Pauta {n}** | Brief {n} (ou o título da 1ª notícia, ver §5) |
| Editor, vazio | No stories in the **pauta** | No stories in this brief |
| Landing, fechamento | Your next **story list** starts here. | Your next brief starts here. |
| Radar e Configurações | Never show — e.g. **celular, games**. | e.g. phones, games. |
| Cadastro | **Sections** (optional) | Beats (optional). "Sections" lembra as seções que saíram do produto |
| Hoje, título × botão | Today's **news** / See today's **stories** | Escolher um: Today's stories |
| Landing | Already a **creator**? Sign in | Already have an account? Sign in |
| Landing e cadastro | …that is also how you get the **launch invitation** | Remover (ver §5) |

---

## 5. Sugestões finais: copy, legendas, heurísticas e UX/UI

Ordenadas pelo que mais pesa no vídeo e na primeira impressão. **Tudo cabe no congelamento**: são ajustes de texto e de comportamento, sem tela nova.

### P0

**1. Nunca bloquear a landing por limite de sessão** (heurística: ajudar o usuário a se recuperar de erros)
- Hoje a landing cria sessão a cada visita, e o limite por IP bloqueia **todo o HTML** com JSON cru.
- **Proposta:**
  - criar sessão só ao clicar "Criar meu Radar" ou abrir `/claim`;
  - a landing, `/termos` e `/privacidade` ficam sempre abertas;
  - subir o limite (CGNAT de celular põe centenas de pessoas atrás de um IP) ou contar por IP + navegador;
  - quando estourar, mostrar uma página do app: "Muita gente entrando ao mesmo tempo daqui. Tente de novo em alguns minutos." com botão "Tentar de novo" e `Retry-After`.

### P1 — confiança e consistência

**2. Não perder texto** (prevenção de erro)
- Salvar ao sair do campo (blur) e ao trocar de rota.
- Reduzir a espera do autosave para ~800 ms.
- Se ainda houver "Salvando…" ao sair, segurar a navegação até salvar ou avisar com `beforeunload`.

**3. Mostrar o que está acontecendo** (visibilidade do estado)
- **"Montando…" (8,5 s):**
  - trocar por etapas curtas ("Juntando as 3 notícias… Abrindo o editor…");
  - ou abrir o editor na hora e carregar as notícias dentro dele.
- **"Copiar":**
  - se o navegador recusar, mostrar "Não deu para copiar — selecione o texto e use Ctrl+C" e já deixar o texto selecionado;
  - hoje falha em silêncio.

**4. Um nome só para a pauta** (consistência)
- O cabeçalho do editor diz "Pauta 1"; a lista mostra o título da 1ª notícia.
- Usar o título da 1ª notícia nos dois lugares e deixar editar clicando no nome.
- No texto exportado nada muda (não vai título automático).

**5. Card de notícia com 4 ações** (reconhecimento em vez de memória; estética minimalista)
- Hoje cada card tem caixa de seleção + Sim + Não + Escrever, e "Sim" **não seleciona**: é fácil achar que "Sim" põe a notícia na pauta.
- **Proposta:**
  - clicar no card marca ou desmarca;
  - Sim/Não viram dois ícones pequenos (👍 👎) com dica "Mais disso" / "Menos disso";
  - "Escrever" sai (já existe "Montar pauta"), ou vira um link discreto "Só esta →".

**6. "Esconder outro?"** (linguagem do usuário)
- Nunca perguntar sobre um tópico interno ("outro", vazio, sem etiqueta). Só perguntar com etiqueta legível ("Esconder Mercado imobiliário?").

**7. Landing: tirar os resquícios do produto antigo**

| Texto atual | Sugestão | Por quê |
|---|---|---|
| "Só o trabalho chato de achar e **conferir** o que aconteceu" | "Só o trabalho chato de achar e organizar o que aconteceu" | "Conferir" promete checagem de fatos, que saiu |
| "…é também assim que recebe o **convite do lançamento**" (hero) e "…você entra na lista do convite do lançamento" (cadastro) | Remover a frase. No cadastro: "Enviamos um link mágico — sem senha." | O produto deve parecer pronto; "convite" diz que ainda não lançou |
| Faixa "Funciona com os feeds que você já lê": Tecnoblog, Olhar Digital, MacMagazine, Meio Bit, Startups.com.br | g1 · InfoMoney · Agência Brasil · Finsiders · Mundo RH · Money Times | Hoje vende só tecnologia; as fontes reais são mais amplas |
| Passo 1: "Diga do que você fala — Seus temas **e fontes**." | "Seus temas. As fontes, a gente escolhe." | Não há escolha de fonte no cadastro |
| "Já sou criador? Entrar" | "Já tenho conta · Entrar" | "Criador" é jargão |
| "pro seu nicho", "Pra publicar" | "para o seu nicho", "Para publicar" | Hoje o registro oscila; o resto do app usa "para" |
| Título quebrando "A pauta / pronta. A / escrita é sua." | Espaço inquebrável depois do "A" (`A&nbsp;escrita`) | Evita o "A" sozinho no fim da linha |

**8. E-mail de confirmação** (prevenção de erro)
- Todos os e-mails têm o mesmo assunto, então o Gmail junta todos numa conversa e o usuário pode clicar num link velho já usado.
- Colocar a hora no assunto ("Seu link do Kloxus — 19h18"), ou um código curto.
- Acrescentar "Se você não pediu, ignore este e-mail."

**9. "Novo Radar" com "um Radar por conta"**
- O menu oferece "Novo Radar", mas a API tem o erro "Esta conta já tem um Radar — um Radar por conta".
- Ou "Novo Radar" funciona, ou sai do menu.
- Também aparece na lateral durante o próprio cadastro.

### P2 — acabamento

**10. Editor**
- Trocar o botão "Comentar" pela caixa já aberta, com rótulo "Seu texto". O subtítulo já diz "Escreva do seu jeito sob cada notícia", e "comentar" sugere algo menor que o texto.
- No celular, a barra de ação e a navegação inferior juntas ocupam ~110 px. Esconder a navegação inferior no editor e no exportar (já existe "← Voltar").

**11. Exportar**
- **Contador geral do X:** quando há sugestão de thread, o "420 / 280 · Acima do limite" em vermelho no topo assusta. Trocar por "Vira uma thread de 4 posts".
- **Contador do post:** colocar o "n/4" numa linha própria, sem colar na URL.
- **Aviso de várias notícias:** "Esta pauta tem 3 notícias. Posts sociais funcionam melhor com uma principal…" aparece sempre. Mostrar só quando LinkedIn ou X estiver marcado, e permitir fechar.
- **Diálogo ao remover:** desmarcar um destino ainda usa a confirmação nativa do navegador. Trocar por "Desfazer" num toast.
- **Caixas de seleção:** estilo nativo azul; usar o mesmo componente da lista de notícias.
- **Pauta exportada:** confirmar se fica **somente leitura** (existe o erro "Esta pauta já foi exportada — é somente leitura"). Se ficar, o usuário não corrige um erro de digitação depois de copiar. Sugiro permitir editar e marcar as versões como "desatualizadas" (o texto "A pauta mudou desde que esta versão foi feita" já existe).

**12. Notícias de hoje**
- **Resumos:** só ~1 em 12 cards tem resumo. Gerar para todos, ou para nenhum: o meio-termo parece falha.
- **Fora do tema:** cerca de 4 em 12 ("Prefeitura de Taubaté troca digital…", "Jensen Huang retorna à Nvidia", "Bolsas de NY…"). Um limiar de similaridade um pouco mais alto resolve sem esvaziar a lista.
- **Datas:** a lista de Pautas usa "24 de nov. de 2026"; o resto usa "há 10 h" e "23/11". Usar "24/11".

**13. Radar e Configurações** (consistência; estética minimalista)
- **Campos repetidos:** as duas telas repetem Nome, Tema e Nunca mostrar. Juntar tudo em **Radar**, com "Apagar Radar" no fim, e tirar "Configurações" do menu (fica Hoje · Pautas · Radar).
- **Editorias:** escolhidas no cadastro, não podem ser editadas depois. Mostrá-las como chips no Radar.
- **Fontes sugeridas:**
  - a lista ainda abre com Tecnoblog, Olhar Digital, MacMagazine e Meio Bit (fora do tema de fintech);
  - cada uma tem a etiqueta "Feed RSS" e o botão "Itens mais recentes", que não explicam nada;
  - ordenar por aderência ao tema;
  - trocar o botão por "Ver últimas";
  - tirar a etiqueta.
- **"Não encontrado":** "…ou pertence a outra sessão" é técnico. Trocar por "Esta página não existe ou é de outra conta."

**14. Heurísticas (Nielsen), resumo**

| Heurística | Situação |
|---|---|
| Visibilidade do estado | "Montando…" longo, cópia silenciosa (3) |
| Correspondência com o mundo real | Pauta vs brief no EN (§4); "outro" (6) |
| Controle e liberdade | Desfazer "Não" em Hoje; sair do editor sem perder texto (2) |
| Consistência | "Pauta 1" vs título (4); datas (12); Radar vs Configurações (13) |
| Prevenção de erro | Perda de texto (2); link velho no e-mail (8) |
| Reconhecimento | Sim vs caixa de seleção (5) |
| Estética minimalista | 4 ações por card (5); duas barras no celular (10) |
| Recuperação de erros | 429 em JSON cru (1) |

---

## 6. Resíduos deste teste

- **Workspace novo** `sthefanocordeiro+kloxus-final-pt@gmail.com`: Radar 1502 "Fintech para bancários", 8 respostas de calibração, pauta 1000 com 3 notícias (abertura + 2 textos de teste), exportada no LinkedIn, com versões X e Markdown.
- **Sessões encerradas com "Sair":** `+kloxus-e2e` (Radar 1500) e `+kloxus-final-pt`.
- **Gmail:** e-mails de confirmação acumulados numa única conversa.
- **Seu IP** está temporariamente bloqueado pelo limite de sessões (P0 §1).

## 7. Pendente

- **Rodada em inglês pela tela:** cadastro → match → pauta → exportar. Roda assim que o 429 cair; os textos já foram revisados no §4.
  - Nova tentativa às 19h58: o site **continuava** respondendo 429 para o mesmo IP, ~30 min depois do bloqueio (19h30). O bloqueio dura pelo menos meia hora, o que reforça o P0 do §1.
- **Não testado:** "Novo Radar", "Apagar Radar", edição de pauta já exportada, download .html, imagem no HTML.
