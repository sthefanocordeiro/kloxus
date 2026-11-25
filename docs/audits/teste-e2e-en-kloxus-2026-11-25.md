# Teste e2e em inglês + pendências — 25/11/2026 (manhã)

**Versão:** bundle `index-B9E6SPKZ.js`

**Como testei:**
- Comecei sem sessão, com a interface em EN e um e-mail novo (`+kloxus-final-en`), abrindo o link mágico pelo Gmail.
- Criei o Radar "Brazil fintech watch" (id 1503), com temas Pix, open finance, Drex, central bank rules, digital banks e as editorias Economy + Financial markets.
- Montei o brief 1001 com 3 notícias e exportei.
- Editei depois de exportar, subi uma imagem no HTML, baixei o .html e apaguei o Radar.

**O 429 da noite caiu:** o site abriu normalmente.

---

## Veredito

**O fluxo em inglês funciona de ponta a ponta:** landing → e-mail → Create Radar → Match → Done! → selecionar notícias → Build brief → Write → Export → Copy.

Os três pontos pedidos passaram:

| Pedido | Resultado |
|---|---|
| 1. Rodada EN pela tela | ✅ Completa. Restam 6 textos em PT vazando e 1 bug de idioma (abaixo) |
| 2. Brief exportado editável | ✅ **Editável, com versões "desatualizadas"** — não ficou somente leitura |
| 3. Download .html, imagem no HTML, Apagar Radar | ✅ Os três funcionam |

**Antes de congelar, só um ponto pesa de verdade:** o idioma escolhido **não sobrevive a recarregar a página**. Quem está em EN volta para PT depois do link mágico e em qualquer recarga.

---

## 1. Rodada em inglês

| Passo | Resultado | Nota |
|---|---|---|
| Landing EN | Traduzida inteira. Faixa de fontes nova (g1, InfoMoney, Agência Brasil, Finsiders, Mundo RH, Money Times). Sem "launch invitation" | ✅ |
| E-mail | Assunto **"Your Kloxus link — 07h39"**: horário no formato brasileiro num e-mail em inglês. Tem "If you didn't ask for this, ignore this email." | 🟡 |
| `/claim` | Em inglês, "Confirm and continue" | ✅ |
| Depois de confirmar | **A interface voltou para PT** ("Crie seu Radar", "Criar Radar · Dar match · Pronto!") | ❌ |
| Create your Radar | "Radar name", "Topics (comma separated)", "Beats (optional)", 18 editorias em inglês. "Novo Radar" saiu da lateral | ✅ |
| Match | Abriu em ~2 s. Cards em português (as fontes são brasileiras, esperado). 5 de 8 no tema. Sem "Hide outro?" | ✅ |
| Done! | "12 stories match your Radar today" → "Build my first brief" | ✅ |
| Today's stories | **Clicar no card seleciona** e 👍/👎 viraram ícones (sugestão de ontem aplicada). Barra "3 stories selected · Build brief →". "Configurações" saiu do menu (Today · Briefs · Radar) | ✅ |
| Build brief | Mostra o andamento: "Putting the 3 stories together…" → "Opening the editor…" (~8 s) | ✅ |
| Write | O cabeçalho e o título usam a 1ª notícia. "Opening (optional)", "Comment", ↑ ↓ ✕, "+ Add story", "Go to export" | ✅ |
| Sair do editor pelo menu logo depois de digitar | O texto **foi salvo** (gravou depois da troca de tela) | ✅ |
| Recarregar logo depois de digitar | O texto digitado **se perdeu** e não apareceu aviso | 🟡 |
| Export | "Where is it going?", "Becomes a thread of 4 posts", "n/4" em linha própria, aviso de várias notícias com ✕. Título linkado em todos os formatos | ✅ |
| Prévia do LinkedIn em EN | A marca diz "SEE MORE cuts here…", mas o fim do texto mostra **"… ver mais"** | 🟡 |
| Copy | "Copied" → lista "Exported · HTML · LinkedIn" | ✅ |

### Português ainda aparecendo em EN

| Onde | Texto | Correção |
|---|---|---|
| Toda a interface depois de recarregar ou confirmar o e-mail | Volta para PT | Guardar o idioma num cookie/localStorage e na conta; o link mágico deve levar `lang` |
| Assunto do e-mail | "07h39" | "7:39 AM" em EN |
| Prévia do LinkedIn | "… ver mais" | "… see more" |
| Download .html | Arquivo `pauta.html` com `<html lang="pt-BR">` | `brief.html` (ou o título em slug) e `lang` conforme a interface |
| Radar → "Radar theme" | "…digital banks, **economia, mercado financeiro**": as editorias entram no campo de tema como texto em PT, **duplicadas** com os chips "Beats" | Guardar as editorias só como chips; não misturar com o tema |
| Today/Match | "0 h ago" | "just now" / "< 1 h ago" |

---

## 2. Brief exportado: editável, com versões desatualizadas

| Teste | Resultado |
|---|---|
| Abrir um brief exportado pela lista | Abre direto no **Export** (`/pauta/1001/export`); "Back to …" no cabeçalho leva ao editor |
| Editar o texto | Caixas editáveis, sem bloqueio |
| Voltar ao Export | As 3 versões ficaram marcadas: **"The brief changed since this version was made." + "Update this version?"** |
| Atualizar | Diálogo do app (não o nativo do navegador): "Updating replaces your edits in this version with the current brief. Continue?" → **Update**. A versão passa a ter o texto novo e deixa de estar desatualizada |
| Status na lista | Continua "Exported" depois de editar. Sugestão: "Edited since export" enquanto houver versão desatualizada |

**Bug encontrado (P1, risco de perder texto):**
1. Editor → Go to export → Briefs → abrir o brief → "Back to …" (navegação dentro do app, sem recarregar).
2. O editor mostrou **a 3ª notícia vazia, com o botão "Comment"**, embora o texto estivesse salvo no servidor.

O editor reaproveita dados antigos guardados na memória da página. Como o salvamento envia **todas** as notícias juntas, digitar em outra notícia nesse estado **apagaria o texto da 3ª**.

**Correção:**
- Recarregar os dados ao abrir o editor (invalidar o cache depois de salvar).
- Salvar só a notícia alterada, em vez do conjunto inteiro.

---

## 3. Itens não testados antes

| Item | Resultado |
|---|---|
| **Imagem no HTML** | ✅ Botão "Image" na barra do editor HTML. PNG de 13 KB enviado (`POST /media` → 201). A imagem aparece no texto e é salva na versão. No .html baixado sai com **URL absoluta** (`https://kloxus.fly.dev/m/…`), então funciona colada em outro lugar |
| **Download .html** | ✅ HTML completo (`<!doctype html>`, `meta charset`, `viewport`, `<title>` = abertura), com títulos linkados e a imagem. Detalhes: nome `pauta.html` e `lang="pt-BR"` em EN (acima) |
| **Apagar Radar** | ✅ Diálogo do app: "Delete this Radar? Brazil fintech watch and everything it owns — briefs and sources — will be deleted. This cannot be undone." → **Yes, delete it** → `DELETE /publications/1503` **204**. O brief passa a dar 404 e a tela volta ao estado vazio |
| Depois de apagar | **A imagem enviada continua acessível** (`/m/4-…` → 200). Apagar o Radar deveria apagar as mídias também (privacidade e espaço) |
| Estado vazio | Três botões para a mesma coisa: "New Radar" no topo, "New Radar" na lateral e "Create your Radar" no card. Deixar só o do card. Também não aparece confirmação de que o Radar foi apagado |

---

## 4. Outras observações (P2)

- **Radar:**
  - o título da seção diz "Radar topics" e o campo diz "Radar theme" — usar "Topics" nos dois;
  - os chips de editorias não são editáveis.
- **Botão "Confirm 19 sources & collect":** "collect" é jargão; sugestão "Add 12 suggested sources". O 19 conta também as 7 já ativas, e confunde.
- **Fontes sugeridas no topo:** Folha e Valor, que têm paywall. Marcar "paywall" ou mandar para o fim.
- **"Comment" no editor:** o subtítulo diz "Write in your own words under each story", mas o botão diz "Comment" — sugestão "Write" / "Your text".
- **Hint de Today's stories:** o texto usa emojis 👍 👎 e os botões usam ícones de traço — padronizar com os ícones. A mensagem de vazio ainda diz "Tick stories and click Build brief" ("Tick" não existe mais).
- **Cabeçalho "Radar":** a palavra aparece em cima do título do brief. Faltam as migalhas "Brazil fintech watch › Brief".
- **Autosave:** depois de 3 s ainda aparecia "Saving…". Encurtar a espera ou mostrar "Saved" mais rápido.

---

## Resíduos

- **Workspace** `sthefanocordeiro+kloxus-final-en@gmail.com`, sem Radar: o Radar 1503 e o brief 1001 foram **apagados** no teste de "Delete Radar".
- **Imagem de teste** `kloxus-audit.png` ainda servida em `/m/4-1aac09d2b97320a8` (órfã).
- **1 e-mail** "Your Kloxus link" no Gmail.
- Interceptei o clique do download para ler o arquivo: **nenhum arquivo foi salvo no seu computador**.
