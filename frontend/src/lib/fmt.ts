import type { Lang } from "../i18n";

// The API stores timestamps as naive UTC ("2026-11-20 14:30:00", no
// zone marker). new Date() reads that as LOCAL time — in Brazil every
// stored time displayed three hours early (KLOX-100). Parse the naive
// shape explicitly as UTC; RFC3339/ISO strings keep their own zone.
const NAIVE_TS = /^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2}(\.\d+)?)?$/;

function parseAPITime(iso: string): Date {
  const s = iso.trim();
  return new Date(NAIVE_TS.test(s) ? s.replace(" ", "T") + "Z" : s);
}

// Reader-locale date formatting (pt-BR / en-US).
export function fmtDate(iso: string, lang: Lang, withTime = false): string {
  const d = parseAPITime(iso);
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
