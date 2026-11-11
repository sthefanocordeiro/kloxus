package delivery

import (
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// TestReaderBodyNormalizesCitationPlacement: "urbana. [1]" renders as
// "urbana [1]." in every reader-facing output.
func TestReaderBodyNormalizesCitationPlacement(t *testing.T) {
	iss := &store.Issue{
		Status:  "approved",
		Subject: "Edição 1",
		BodyMD:  "# Pub — issue 1\n\n## S\n\n### H <!-- item 1 -->\n\nO custo segue três vezes o da banda larga urbana. [1] Outra frase aqui.",
	}
	pub := &store.Publication{}
	md := ReaderBody(iss, pub, "")
	if strings.Contains(md, "urbana. [1]") {
		t.Fatalf("citation not normalised:\n%s", md)
	}
	if !strings.Contains(md, "urbana [1].") {
		t.Fatalf("expected 'urbana [1].':\n%s", md)
	}
	// already-correct placement is untouched
	if !strings.Contains(md, "Outra frase aqui.") {
		t.Fatal("unrelated sentence mangled")
	}
}
