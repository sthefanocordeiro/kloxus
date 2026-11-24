package web

import (
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// KLOX-152: a disabled catalog source (TabNews, Hacker News) must not be
// offered or picked, whatever its policy says.
func TestPickable(t *testing.T) {
	cases := []struct {
		policy  string
		enabled bool
		want    bool
	}{
		{"launch", true, true},
		{"signal", true, true},
		{"launch", false, false},
		{"signal", false, false},
		{"hold", true, false},
		{"blocked", true, false},
		{"hold", false, false},
	}
	for _, c := range cases {
		cs := &store.CatalogSource{Name: "X", Policy: c.policy, Enabled: c.enabled}
		if got := pickable(cs); got != c.want {
			t.Errorf("pickable(policy=%s enabled=%v) = %v, want %v", c.policy, c.enabled, got, c.want)
		}
	}
}

func TestNotPickableMsg(t *testing.T) {
	off := &store.CatalogSource{Name: "TabNews", Policy: "launch", Enabled: false, Note: "forum"}
	if m := notPickableMsg(off); !strings.Contains(m, "disabled") || strings.Contains(m, "is launch") {
		t.Fatalf("disabled source message = %q", m)
	}
	held := &store.CatalogSource{Name: "Exame", Policy: "hold", Enabled: true, Note: "pending"}
	if m := notPickableMsg(held); !strings.Contains(m, "is hold") {
		t.Fatalf("held source message = %q", m)
	}
}
