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

// Run-cost formatting: cents for normal values, sub-cent precision
// under $0.01 so a cheap run never reads "$0.00".
export function fmtUSD(v: number): string {
  if (v === 0) return "US$ 0.00";
  return "US$ " + (v >= 0.01 ? v.toFixed(2) : v.toFixed(4));
}
