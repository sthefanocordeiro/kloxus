package pipeline

import (
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// KLOX-274: once a publication is loaded from Postgres, explicit editoria
// state is authoritative even when empty. Labels in free text remain
// subjects; they never select a chip or synthesize editoria evidence.
func TestTopicRolesExplicitNichesAreAuthoritative(t *testing.T) {
	empty := []string{}
	pub := &store.Publication{Topic: "agronegócio familiar, pecuária", Niches: empty}
	ed, subj := topicRoles(pub)
	if ed != "" {
		t.Fatalf("editorias = %q, want none for explicit empty state", ed)
	}
	if subj != "agronegócio familiar, pecuária" {
		t.Fatalf("subjects = %q", subj)
	}

	pub.Topic = "agronegócio"
	ed, subj = topicRoles(pub)
	if ed != "" || subj != "agronegócio" {
		t.Fatalf("exact unselected label roles = (%q, %q), want subject only", ed, subj)
	}

	pub = &store.Publication{
		Topic:  "agronegócio familiar, pecuária, agronegócio",
		Niches: []string{"agronegocio"},
	}
	ed, subj = topicRoles(pub)
	if ed != "agronegócio" {
		t.Fatalf("selected editoria = %q", ed)
	}
	if subj != "agronegócio familiar, pecuária" {
		t.Fatalf("selected subjects = %q", subj)
	}
}

func TestTopicRolesNilNichesKeepLegacyInference(t *testing.T) {
	ed, subj := topicRoles(&store.Publication{Topic: "agronegócio familiar, pecuária"})
	if ed != "agronegócio" {
		t.Fatalf("legacy editorias = %q", ed)
	}
	if subj != "agronegócio familiar, pecuária" {
		t.Fatalf("legacy subjects = %q", subj)
	}
}
