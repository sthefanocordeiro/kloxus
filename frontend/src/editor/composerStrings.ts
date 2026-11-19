import { useT } from "../i18n";

// Composer strings (KLOX-66 D3): only the issue composer imports this
// module, so the strings stay out of the landing chunk — the shared
// i18n table is part of the 150 KiB budget (KLOX-16).
const en = {
  image: "Image",
  embed: "Embed",
  message: "Message",
  bold: "Bold",
  italic: "Italic",
  linkOnly: "link only",
  linkOnlyOn: "Ship as link only",
  linkOnlyOff: "Write text",
  imageTitle: "Insert image",
  imageUrlPh: "https://… or upload",
  altPh: "Alt text",
  upload: "Upload",
  uploadErr: "Upload failed — PNG/JPEG/GIF/WebP up to 2 MB.",
  embedTitle: "Insert embed",
  embedPh: "<iframe …> or trusted HTML",
  embedHint: "Unsafe markup is stripped.",
  msgTitle: "Standard messages",
  msgEmpty: "No saved messages yet.",
  msgNew: "New message",
  msgNamePh: "Name",
  msgBodyPh: "Text…",
  msgDelete: "Delete",
};

const pt: typeof en = {
  image: "Imagem",
  embed: "Incorporar",
  message: "Mensagem",
  bold: "Negrito",
  italic: "Itálico",
  linkOnly: "só link",
  linkOnlyOn: "Enviar só o link",
  linkOnlyOff: "Escrever texto",
  imageTitle: "Inserir imagem",
  imageUrlPh: "https://… ou enviar arquivo",
  altPh: "Texto alternativo",
  upload: "Enviar",
  uploadErr: "Falha no envio — PNG/JPEG/GIF/WebP até 2 MB.",
  embedTitle: "Inserir incorporação",
  embedPh: "<iframe …> ou HTML confiável",
  embedHint: "Marcação insegura é removida.",
  msgTitle: "Mensagens padrão",
  msgEmpty: "Nenhuma mensagem salva.",
  msgNew: "Nova mensagem",
  msgNamePh: "Nome",
  msgBodyPh: "Texto…",
  msgDelete: "Excluir",
};

export type ComposerStrings = typeof en;

// useComposer returns the shared blocks table plus the composer-only
// strings, so call sites keep one `blocks.x` accessor.
export function useComposer() {
  const { t, lang } = useT();
  return {
    ...t.blocks,
    ...(lang === "pt" ? pt : en),
  };
}
