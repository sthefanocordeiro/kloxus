import { useT } from "../i18n";

// Leitores-screen strings for the copyable subscribe link (KLOX-102):
// only the readers page imports this module, so the strings stay out of
// the landing chunk — the shared i18n table is part of the 150 KiB
// budget (KLOX-16, same split as readerStrings.ts).
const en = {
  subscribeLink: "Subscribe link",
  subscribeLinkHint: "Share this link — anyone can subscribe from it.",
  copy: "Copy",
  copied: "Copied!",
};

const pt: typeof en = {
  subscribeLink: "Link de inscrição",
  subscribeLinkHint: "Compartilhe este link — qualquer pessoa pode assinar por ele.",
  copy: "Copiar",
  copied: "Copiado!",
};

export type ReadersPageStrings = typeof en;

// useReadersPage returns the shared readersPage.* table plus the
// subscribe-link strings, so call sites keep one accessor.
export function useReadersPage() {
  const { t, lang } = useT();
  return {
    ...t.readersPage,
    ...(lang === "pt" ? pt : en),
  };
}
