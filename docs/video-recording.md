# Gravação do vídeo HackBrazil

Este documento descreve como regravar as cenas mudas do vídeo de submissão com o gravador Playwright de KLOX-269.

## O que o gravador produz

`frontend/scripts/record-videos.mjs` grava **um arquivo por cena**, em PT-BR, com cursor virtual injetado na página e sem áudio:

| Cena | Arquivo | Duração-alvo | Conteúdo |
|---:|---|---:|---|
| 1 | `cena-1-hook.mp4` | 8 s | landing e promessa principal |
| 2 | `cena-2-problema.mp4` | 12 s | problema / movimento de leitura na landing |
| 3 | `cena-3-cadastro-magic-link.mp4` | 15 s | CTA, e-mail, magic link e confirmação |
| 4 | `cena-4-radar-match.mp4` | 15 s | criação do Radar e calibração com match |
| 5 | `cena-5-hoje-montar-pauta.mp4` | 30 s | Today, escolha de 3 notícias e montagem da pauta |
| 6 | `cena-6-escrever.mp4` | 40 s | editor, escrita manual e autosave |
| 7 | `cena-7-exportar-copiado.mp4` | 25 s | formatos de exportação e estado “Copiado” |
| 8 | `cena-8-cta.mp4` | 15 s | fechamento com `kloxus.fly.dev` |

A soma nominal é **160 s**, abaixo do limite de 3 minutos. O script também grava `manifest.json` com duração, viewport, data e caminho dos arquivos.

Por segurança, o gravador **se recusa a rodar contra produção ou contra um servidor com e-mail real**. O alvo validado é o modo demo local (`mode=demo`, `mail=outbox`), que usa fixtures determinísticas, não depende de Gmail/Resend e não chama IA paga.

## Pré-requisitos

1. Go, Node.js e PostgreSQL descartável.
2. Dependências e build do frontend:

   ```sh
   cd frontend
   npm install
   npm run build
   ```

3. Chromium do Playwright instalado:

   ```sh
   npx playwright install chromium
   ```

4. `ffmpeg` e `ffprobe` no `PATH` para converter WebM → MP4 e medir as durações. Sem `ffmpeg`, o script mantém os `.webm` e avisa no final.

## Gravar todas as cenas

Se não houver servidor demo rodando, exporte `DATABASE_URL` e deixe o script subir o servidor local na porta 8137:

```sh
cd frontend
DATABASE_URL='postgres://postgres:postgres@localhost:55432/kloxus?sslmode=disable' npm run video:record
```

No PowerShell, exporte a variável antes:

```powershell
$env:DATABASE_URL='postgres://postgres:postgres@localhost:55432/kloxus?sslmode=disable'
cd frontend
npm run video:record
```

Se o servidor demo já estiver rodando em `http://localhost:8137`, reutilize-o:

```sh
cd frontend
npm run video:record -- --no-server
```

Saída padrão:

```text
frontend/recordings/hackbrazil/
├── cena-1-hook.mp4
├── ...
├── cena-8-cta.mp4
└── manifest.json
```

Os arquivos `.webm` intermediários são removidos depois da conversão. Para preservá-los:

```sh
npm run video:record -- --keep-webm
```

## Regravar uma cena

Use `--scene N`. As cenas dependentes de estado refazem automaticamente o fluxo mutante anterior **sem gravar** (cadastro → Radar → Today/pauta → escrita, conforme necessário) e gravam somente a cena pedida:

```sh
cd frontend
npm run video:record -- --scene 6 --no-server
npm run video:record -- --scene 7 --no-server
```

Exemplos:

- `--scene 3` regrava só cadastro/magic link.
- `--scene 5` prepara cadastro + Radar e grava Today/montagem da pauta.
- `--scene 7` prepara todo o fluxo até a escrita e grava só exportação/cópia.
- `--scene 8` prepara o fluxo completo e grava só o fechamento.

Cada execução começa em um navegador limpo, então não é preciso limpar cookies nem reutilizar a conta de uma gravação anterior.

## Opções úteis

- `--base-url http://localhost:8137` ou `KLOXUS_RECORD_BASE_URL`: troca o alvo local.
- `--out-dir <pasta>` ou `KLOXUS_RECORD_OUT`: troca a pasta de saída.
- `KLOXUS_RECORD_EMAIL=criador@exemplo.com`: troca o e-mail exibido no cadastro.
- `--fast` ou `KLOXUS_RECORD_FAST=1`: validação rápida de seletores/fluxo, sem cumprir as durações-alvo. Use só para teste, não para o take final.
- `--no-server`: não tenta subir `go run ../cmd/kloxus demo`; exige servidor já saudável no `--base-url`.

## Dados do take

O take segue os dados combinados no storyboard:

- Radar: **Fintech para bancários**
- Temas: **Pix, open finance, Drex, regulação do BC**
- Seleção: 3 notícias no Today
- Texto manual no editor; a IA não escreve comentário
- Exportação: LinkedIn, X, newsletter/HTML e Markdown; cópia final em LinkedIn

## Se um seletor quebrar

1. Rode `npm run video:record -- --fast --keep-webm` para reproduzir rápido e inspecionar os WebM.
2. Compare o seletor em `frontend/scripts/record-videos.mjs` com o fluxo de referência em `frontend/e2e/radar.spec.ts` e com os componentes em `frontend/src/components/`.
3. Ajuste a cena específica e regrave com `--scene N`; não é necessário repetir manualmente as cenas anteriores.
