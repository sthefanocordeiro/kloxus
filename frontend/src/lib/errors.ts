import { ApiRequestError } from "../api";
import type { T } from "../i18n";

// Localized text for an API error: known codes translate, everything
// else falls back to the server's own message, then the generic line.
export function errText(t: T, e: unknown): string {
  const dict = t.errors as Record<string, string>;
  if (e instanceof ApiRequestError) {
    return dict[e.body.code] ?? e.body.message ?? dict.generic;
  }
  return dict.generic;
}

// The collect step's empty-harvest failure (pipeline.go) — recovery
// lives on the publication's sources page, so callers share the check.
export function isNoItemsFailure(msg?: string): boolean {
  return !!msg?.startsWith("no items collected");
}

// A finished run carries a bare message, not a coded ApiRequestError —
// map the known pipeline failures onto the i18n table (KLOX-90).
export function runErrText(t: T, msg?: string): string {
  if (isNoItemsFailure(msg)) return t.run.noItems;
  return msg || (t.errors as Record<string, string>).generic;
}
