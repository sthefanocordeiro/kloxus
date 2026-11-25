package strutil

import "testing"

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 10); got != "short" {
		t.Fatalf("short string changed: %q", got)
	}
	if got := Truncate("  padded  ", 10); got != "padded" {
		t.Fatalf("edges not trimmed: %q", got)
	}
	if got := Truncate("abcdef", 3); got != "abc…" {
		t.Fatalf("cut: %q", got)
	}
	// Rune-safe: a 2-byte rune at the boundary must not be split.
	got := Truncate("abçd", 3)
	if got != "abç…" {
		t.Fatalf("rune boundary split: %q", got)
	}
}

func TestCut(t *testing.T) {
	if got := Cut("short", 10); got != "short" {
		t.Fatalf("short string changed: %q", got)
	}
	if got := Cut("abcdef", 3); got != "abc…" {
		t.Fatalf("cut: %q", got)
	}
	// Byte-exact for replay stability: "ç" is 2 bytes, so a byte cut
	// can split it — that is the documented contract of Cut.
	if got := Cut("abçd", 3); got != "ab\xc3…" {
		t.Fatalf("byte cut: %q", got)
	}
}
