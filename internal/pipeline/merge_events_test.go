package pipeline

import (
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func TestMergeEventGroups(t *testing.T) {
	d := "2026-11-20T10:00:00Z"
	g := map[int][]store.Item{
		0: {{ID: 1, Title: "GTA 6 bate recorde de pré-venda", PublishedAt: d, URL: "https://a.com/1"}},
		1: {{ID: 2, Title: "Pré-venda de GTA 6 bate recorde histórico", PublishedAt: d, URL: "https://b.com/2"}},
		2: {{ID: 3, Title: "Banco Central regulamenta Pix parcelado", PublishedAt: d, URL: "https://c.com/3"}},
		3: {{ID: 4, Title: "GTA 6 recorde de pré-venda confirmado", PublishedAt: "2026-01-01T00:00:00Z", URL: "https://d.com/4"}},
	}
	out := mergeEventGroups(g)
	if len(out) != 3 {
		t.Fatalf("want 3 stories (GTA pair merged, old one apart), got %d", len(out))
	}
}
