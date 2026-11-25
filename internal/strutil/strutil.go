// Package strutil holds small string helpers shared across packages.
package strutil

import (
	"strings"
	"unicode/utf8"
)

// Truncate shortens s to at most n runes for excerpts and log lines,
// appending "…" when it cuts. Rune-safe: never splits a UTF-8 sequence.
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// Cut shortens s to at most n bytes, appending "…" when it cuts.
// Byte-exact: use it for strings that feed recorded LLM requests —
// fixture keys hash the request text, so the cut must not change shape.
func Cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
