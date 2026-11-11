import type { Lang } from "../i18n";

// Reader-locale date formatting (pt-BR / en-US).
export function fmtDate(iso: string, lang: Lang, withTime = false): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return new Intl.DateTimeFormat(lang === "pt" ? "pt-BR" : "en-US", {
    dateStyle: "medium",
    ...(withTime ? { timeStyle: "short" as const } : {}),
  }).format(d);
}
