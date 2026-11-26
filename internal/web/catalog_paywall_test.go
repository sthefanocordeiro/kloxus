package web_test

// Contract test for the real-mode catalog paywall surface (KLOX-235):
// the reader-paywalled outlets the migrations seed (Folha, Valor) come
// flagged in the offered list and sorted after every open suggestion.

import (
	"fmt"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/testdb"
	"github.com/sthefanocordeiro/kloxus/internal/web"
)

func TestRealCatalogPaywallLast(t *testing.T) {
	pool := testdb.Pool(t)
	srv, _, pub, cfg := demoServerOpt(t, func(d *web.Deps) { d.Pool = pool })
	cfg.Mode = "real"

	offered, _ := apiGet(t, srv,
		"/api/v1/publications/"+fmt.Sprint(pub.ID)+"/sources/catalog")["offered"].([]any)
	if len(offered) == 0 {
		t.Fatal("catalog offers nothing")
	}

	flagged := map[string]bool{}
	seenPaywall := false
	for _, x := range offered {
		row := x.(map[string]any)
		pw, _ := row["paywall"].(bool)
		if seenPaywall && !pw {
			t.Fatalf("open source %v offered after a paywalled one", row["url"])
		}
		seenPaywall = seenPaywall || pw
		switch row["url"] {
		case "https://www.folha.uol.com.br":
			flagged["folha"] = pw
		case "https://valor.globo.com":
			flagged["valor"] = pw
		}
	}
	if !flagged["folha"] || !flagged["valor"] {
		t.Fatalf("folha/valor must carry paywall: got %v", flagged)
	}
}
