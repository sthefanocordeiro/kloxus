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
