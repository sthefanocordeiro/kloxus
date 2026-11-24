# Auditoria MLP da Kloxus, 20/11/2026

Alvo: https://kloxus.fly.dev/ (bundle `index-DGWFEznr.js`), testado entre 07:48 e 08:15 (-03:00) no navegador integrado, como visitante novo (o site criou um espaço novo), em português, a 1440 px e 390 px, com uma passada em inglês.

Referência: a visão de produto em `claude/prompt-kloxus-design-e-ia.md` (16/11) e o critério de MLP: o primeiro uso precisa entregar um momento que faça o criador querer voltar.

Para testar como um criador real, **criei uma newsletter própria sobre fintech** ("Radar Fintech MLP": fintechs, bancos digitais, Pix, cripto e regulação; pular promoções, lançamentos de celular e games), com seções personalizadas, e levei a edição até o envio de teste.

## Veredito

**Produto utilizável, ainda não amável.** A casca está pronta e bonita: landing profissional, navegação clara, montador de seções e passos com contador. Mas o momento que faria alguém se apaixonar não acontece. Quem cria uma newsletter de fintech recebe como pauta principal "Um servidor de arquivos para a rede toda com um único binário", sem resumo, e escreve tudo à mão numa caixa estreita. A IA continua desligada (`llm_mode: off`), e sem ela a promessa da landing ("separa o que vale a pena e prepara a pauta") não se cumpre.

Em uma frase: o design avançou, a jornada fecha de ponta a ponta, e o valor central ainda não existe.

## Placar contra a visão de produto

| Área da visão | Estado | Observação |
|---|---|---|
| 1. Landing sem promessas, estilo beehiiv | **Entregue** | Hero, três passos, três cartões, perguntas, rodapé com Termos e Privacidade. Sem dossiê nem patrocinadores |
| 2. Português natural | **Quase** | Restos: "Montar a edição · decisões", "Nota de relevância: 6.9/10", "em desenvolvimento" |
| 3. Navegação e passos | **Entregue em parte** | Barra lateral, "Voltar" de verdade e contador no cadastro e na edição. Falta progresso ("3 de 9 escritas") e escrever uma pauta por vez |
| 3.4 Faixa de espaço anônimo | **Entregue** | Some; o e-mail só é pedido no envio real |
| 4. Visual (cores, tipos, botões) | **Entregue em parte** | Paleta Floresta e lima, Manrope/Inter/Source Serif, botões de 40–44 px. Ainda há alvos pequenos e duas ações principais na mesma tela |
| 4.5 Sem custo, sem jargão técnico | **Quase** | Custo sumiu; a nota "6.9/10" voltou no painel de fontes |
| 5. Seções do jeito do criador | **Entregue** | Nome, formato, quantidade, comentário, acesso, modelos, total ao vivo e prévia. É o ponto alto |
| 6. Telas da edição | **Parcial** | Falta editar frase aceita; a escrita acontece num painel lateral; a etapa 3 tem sete botões |
| 7. IA centralizada e barata | **Não entregue** | Desligada. Cartões sem resumo, sem tipo de matéria; o tema da newsletter não muda nada |
| 8. Pendências da v100 | **Metade** | Detalhes abaixo |

## O que já é amável

- **Primeira impressão.** A landing parece produto pronto. "A pauta pronta. A escrita é sua." funciona, o verde-lima é distinto e a página carrega rápido.
- **Montador de seções.** Renomeei, mudei quantidades, criei uma seção "Regulação" e vi a prévia e o total ("Sua edição terá 9 pautas") mudarem na hora. É o tipo de controle que o criador sente como dele.
- **Montar a edição.** Duas colunas, vagas por seção ("2/2 · cheia"), "Mover para…" e "Desfazer" no aviso. Remover pede motivo com um clique.
- **Regra de citação visível.** Frase sem fonte, nome que não está na fonte ("Financial Times") e cópia literal aparecem sinalizadas no lugar, com Editar, Marcar como interpretação e Remover, além da remoção em lote.
- **Envio de teste.** Vai para a caixa de saída, a edição continua editável, e aparecem página do leitor, downloads (HTML, Markdown, PDF) e "Preparar próxima edição".
- **E-mail e página do leitor.** Source Serif, sem Markdown cru, com preço e o que a assinatura inclui. O título da aba é o da edição.
- **Configurações.** Editar tudo, preço e apagar a newsletter com aviso de que não há volta.
- **Inglês.** A interface troca por inteiro; só o conteúdo fica em português, como deve.

## P0 — o que impede o "amável"

### 1. A pauta não tem nada a ver com a newsletter
A ordem das sugestões é só por data. Resultado para uma newsletter de fintech:

| Seção | Pauta sugerida |
|---|---|
| A grande notícia | Um servidor de arquivos para a rede toda com um único binário (TabNews) |
| Vale saber | Como desativar o Modo Pouca Energia… do iPhone · Ações da Anthropic caem quase 6% |
| Links rápidos | Lua hoje: confira a fase da Lua · Apple revela detalhes sobre seu agente de IA |
| Regulação | Gerador de Dossiê KYB de CNPJ (post de divulgação) · Google leva histórico de IA à página inicial · A coragem vem antes da manchete |

A linha editorial ("pule lançamentos de celular e games") foi ignorada: entraram iPhone e "já jogou GTA hoje?". A única pauta realmente de fintech ("vazamento de dados de clientes da Visa, Mastercard e JPMorgan") estava escondida em Links rápidos.

Duas causas:
- **A IA está desligada.** Sem a leitura central de cada artigo (resumo, tipo, assuntos), não há como casar pauta com tema.
- **A seção não tem assunto.** O criador nomeia "Regulação", mas o motor não sabe o que isso quer dizer e preenche com qualquer coisa.

### 2. Os cartões de sugestão não dizem nada
Só título e veículo. Sem resumo, sem horário no cartão, sem tipo ("tutorial", "opinião"). Metade das 50 primeiras está em inglês (Hacker News). Para decidir, o criador precisa abrir cada uma.

### 3. Escrever é trabalho braçal num espaço apertado
- O texto é digitado numa caixa de ~90 px de altura no painel da direita, não no documento.
- **Frases aceitas não podem ser editadas nem removidas** (nem com duplo clique ou passando o mouse). Só as sinalizadas têm botões. Pendência desde a v100.
- O painel "Fontes e fatos" é cópia do texto da fonte, com subtítulo grudado ("Incidentes envolveram fornecedores terceirizados Grupo afirma…") e a nota "6.9/10".
- Não há "Escrevendo 3 de 9" nem foco em uma pauta por vez.

### 4. A tela ainda diz que o produto não está pronto
- "Esta função ainda não foi lançada — está em desenvolvimento." (colar link da newsletter, no passo 1)
- Selo "em desenvolvimento" e parágrafo sobre revisão interna em "Adicionar fonte própria"
- "Teste sem cadastro." aparece como pílula com ícone e parece botão, mas não é clicável.

### 5. O catálogo não atende o tema que o criador escolhe
As sete fontes são de tecnologia geral. Uma newsletter de fintech, cripto ou regulação não tem de onde tirar pauta. Com a IA desligada, a tela não avisa isso.

## P1 — bugs e falhas de jornada

1. **Horário errado, de novo.** O envio de 07:54 aparece como 10:54 na edição, na caixa de saída e no recibo.
2. **"Carregar mais sugestões" não carrega nada.** A API ignora o parâmetro de página: a página 1 devolve as mesmas 59 da página 0, e a lista continua com 50 cartões.
3. **Segunda edição bloqueada.** Vinte minutos depois da primeira, "Preparar próxima edição" respondeu "Nada novo nas fontes desde a última edição", embora houvesse cerca de 140 sugestões não usadas. A tela usa textos antigos ("Montar a edição · decisões", link "Publicações").
4. **Checagem de fonte ainda deixa passar:**
   - data errada ("dados obtidos em 3 de novembro"; a fonte diz 16);
   - sentido invertido ("As empresas negaram qualquer incidente"; a fonte diz que confirmaram).

   A manchete também não conta como apoio: "A coragem vem antes da manchete", que é o próprio título, foi sinalizada.
5. **Guia editorial genérico.** Rejeitei com "não é para meu público" e o guia gravou só esse rótulo, sem dizer o tipo, o assunto ou o veículo rejeitado.
6. **"Só 2 sugestões para Links rápidos hoje"** apareceu quando esvaziei uma vaga, com 140 sugestões disponíveis. A mensagem induz ao erro.
7. **Etapa 3 congestionada.** Sete ações lado a lado: Editar, Enviar teste, Enviar (em vermelho, como se fosse destrutivo), Agendar envio, Exportar, Prévia e o selo "Aprovada". Depois do teste surge um segundo botão principal, "Preparar próxima edição". No celular, as ações ocupam meia tela em quatro linhas.
8. **Não dá para ir à etapa 3 pelo contador.** Clicar em "Revisar e enviar" não faz nada; o caminho é "Aprovar edição".
9. **Rótulos que se contradizem.** Na lista da etapa Escrever, a mesma pauta aparece como "aprovada" e, segundos depois, como "a decidir".
10. **Título duplicado.** O topo mostra "Edição 1" e, logo abaixo, "#1 · Radar Fintech MLP — edição 1".
11. **Fonte bloqueada pela política de segurança.** O navegador registra seis erros ao carregar uma fonte embutida (`data:font/woff2`). Algum ícone ou texto pode estar caindo para a fonte reserva.
12. **Prévia do e-mail em branco em tela estreita.** A 743 px, o quadro da caixa de saída ficou vazio (a 1440 px renderizou). Vi uma vez.

## Leitores: o elo que falta para um MLP

- A tela **Leitores** mostra "0 leitores confirmados · 0 assinantes" e um link para a caixa de saída. Não há como convidar, importar uma lista ou copiar um link de inscrição.
- A página do leitor exibe o cabeçalho "Kloxus · Visão do leitor", não a marca da newsletter. "Visão do leitor" é termo de bastidor.
- O leitor lê "assine para ler esta e mais 7 **pautas**": "pauta" é palavra do criador; para o leitor é "matérias".
- Não existe página pública da newsletter (`/p/radar-fintech-mlp` dá "Não encontrado"), nem arquivo de edições.
- O preço R$ 12/mês veio preenchido sem eu escolher.

## P2 — acabamento

- Alvos pequenos: selo de citação 20 px; alternância de prévia (assinante/não assinante, e-mail/web/celular) 24 px; links "HTML · Markdown · PDF" 15 px; caixa "minha leitura — sem fonte" 13 px; perguntas da landing 24 px.
- Perguntas da landing com o triângulo padrão do navegador, sem estilo.
- "Lê fontes reais como Tecnoblog, Olhar Digital…" na landing parece endosso dos veículos. Vale trocar por algo como "Funciona com os feeds que você já lê".
- Seletor de idioma no cabeçalho; a visão pedia no rodapé.

## O que não foi testado

- Salvar com e-mail e link de acesso; envio real a leitores; agendamento.
- Assinatura e pagamento na página do leitor.
- Editar depois de aprovar (aviso de nova aprovação).
- Arrastar e soltar; atalhos de teclado.
- Uso real de IA, custo e cache: a IA está desligada.
- Código, testes e CI.

## Para virar MLP antes do vídeo (24/11)

Em ordem de impacto no "amável":

1. **Ligar a leitura central por IA** (seção 7.2.2 do documento de 16/11): resumo de uma linha, tipo e assuntos de cada artigo, uma vez para todos. Usar os assuntos para ordenar pelo tema e pela linha editorial. Sem isso, nada abaixo resolve o P0-1.
2. **Dar assunto à seção.** No montador, um campo "Que tipo de pauta entra aqui?" (palavras-chave ou assuntos da lista). O motor preenche cada seção pelo assunto, não pela ordem.
3. **Cartão de sugestão com resumo, hora e tipo**, e esconder por padrão o que está em inglês ou fora do tema, com um filtro para mostrar.
4. **Escrever no documento.** Clicar em "Escreva esta pauta" abre o texto ali mesmo, em tela larga; toda frase pode ser editada e removida; "Escrevendo 3 de 9". Um clique em um fato do painel o insere como frase com fonte, para o criador reescrever.
5. **Tirar "em desenvolvimento"** das telas e transformar "Teste sem cadastro" em texto simples.
6. **Etapa 3 com uma ação principal.** "Enviar" verde à direita; "Enviar teste", "Agendar" e "Exportar" num menu; o resto some.
7. **Leitores:** link de inscrição para copiar, página pública com a marca da newsletter e o arquivo de edições.
8. **Bugs rápidos:** fuso horário, paginação, "Nada novo" com sugestões sobrando, datas e números na checagem, manchete como apoio.

Os itens 1 a 4 são o que separa "funciona" de "quero usar toda semana".

## Resíduo do teste

- Espaço novo `visitor ad91…`, publicação 1052 "Radar Fintech MLP".
- Edição 779 aprovada, com um envio de teste para a caixa de saída simulada e meu comentário "COMENTARIO-MLP".
- Uma tentativa de edição 2 parou em "Nada novo nas fontes".
- Nenhum e-mail real foi enviado.
