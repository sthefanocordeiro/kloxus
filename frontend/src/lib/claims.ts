// Mirror of the server's pipeline.HasNumberDateOrQuote (textquality.go):
// a sentence with a number, a date word or quotation marks is a factual
// claim — the server refuses to mark it as interpretation, so the UI
// does not offer the action (KLOX-124).
const DATE_WORDS =
  "janeiro|fevereiro|março|marco|abril|maio|junho|julho|agosto|setembro|outubro|novembro|dezembro|segunda|terça|terca|quarta|quinta|sexta|sábado|sabado|domingo|hoje|amanhã|amanha|ontem|january|february|march|april|may|june|july|august|september|october|november|december|monday|tuesday|wednesday|thursday|friday|saturday|sunday|today|tomorrow|yesterday";
const WORD = String.raw`[\p{L}\p{N}_]`;
const DATE_RE = new RegExp(`(?<!${WORD})(?:${DATE_WORDS})(?!${WORD})`, "iu");

export function hasNumberDateOrQuote(s: string): boolean {
  return /\d/.test(s) || DATE_RE.test(s) || /["«»“”‘’]/.test(s);
}
