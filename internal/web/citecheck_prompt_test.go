package web

import (
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
)

func TestEditCitecheckPromptRendersFields(t *testing.T) {
	d := &pipeline.Dossier{Sources: []pipeline.DossierSource{{Ref: 3, URL: "https://example.com/a"}}}
	got, err := editCitecheckPrompt(d, 7, "Prefeitura anunciou obra [3].")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "no value") {
		t.Fatalf("prompt has unrendered fields:\n%s", got)
	}
	for _, want := range []string{"[1] (item 7,", "Prefeitura anunciou obra [3].", "item 7: [3] https://example.com/a"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
}
