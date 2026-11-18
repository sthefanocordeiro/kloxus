package web

// KLOX-65 builder validation: the rules the section builder enforces —
// a named section of 1–10 stories, a known format/commentary/access,
// unique ids and titles, and a 20-story edition ceiling — are enforced
// server-side too, so a hand-rolled request cannot store what the UI
// would never let the creator build.
import (
	"strings"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

func sec(title string, items int) config.Section {
	return config.Section{Title: title, Items: items, Commentary: "optional"}
}

func reasonCodes(rs []apiReason) map[string]bool {
	out := map[string]bool{}
	for _, r := range rs {
		out[r.Code] = true
	}
	return out
}

func TestValidateSectionsOK(t *testing.T) {
	got, reasons := validateSections([]config.Section{
		sec("O destaque", 1), sec("Vale saber", 4), sec("Links", 5),
	})
	if len(reasons) != 0 {
		t.Fatalf("valid list rejected: %+v", reasons)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 sections, got %d", len(got))
	}
	for _, s := range got {
		if s.ID == "" || s.Format == "" || s.Access == "" {
			t.Errorf("section not normalized: %+v", s)
		}
	}
	if got[0].ID == got[1].ID {
		t.Errorf("ids not unique: %q", got[0].ID)
	}
}

func TestValidateSectionsRules(t *testing.T) {
	long := strings.Repeat("x", 41)
	cases := []struct {
		name string
		in   []config.Section
		want string
	}{
		{"empty", nil, "sections_empty"},
		{"blank title", []config.Section{sec("  ", 3)}, "section_title_empty"},
		{"long title", []config.Section{sec(long, 3)}, "section_title_long"},
		{"dup title", []config.Section{sec("A", 2), sec("a", 2)}, "section_title_dup"},
		{"zero items", []config.Section{sec("A", 0)}, "section_items"},
		{"eleven items", []config.Section{sec("A", 11)}, "section_items"},
		{"bad format", []config.Section{{Title: "A", Format: "x", Items: 2, Commentary: "optional"}}, "section_format"},
		{"bad commentary", []config.Section{{Title: "A", Items: 2, Commentary: "x"}}, "section_commentary"},
		{"bad access", []config.Section{{Title: "A", Items: 2, Commentary: "optional", Access: "x"}}, "section_access"},
		{"dup ids", []config.Section{
			{ID: "same", Title: "A", Items: 2, Commentary: "optional"},
			{ID: "same", Title: "B", Items: 2, Commentary: "optional"},
		}, "section_id_dup"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, reasons := validateSections(tc.in)
			if !reasonCodes(reasons)[tc.want] {
				t.Errorf("want reason %q, got %+v", tc.want, reasons)
			}
		})
	}
}

func TestValidateSectionsTotalCap(t *testing.T) {
	in := []config.Section{sec("A", 10), sec("B", 10), sec("C", 1)}
	_, reasons := validateSections(in)
	if !reasonCodes(reasons)["sections_total"] {
		t.Errorf("want sections_total, got %+v", reasons)
	}
	// exactly 20 passes
	ok := []config.Section{sec("A", 10), sec("B", 10)}
	if _, reasons := validateSections(ok); len(reasons) != 0 {
		t.Errorf("20 stories should pass, got %+v", reasons)
	}
}

func TestIssueSectionsSnapshotFallback(t *testing.T) {
	iss := &store.Issue{}
	pub := &store.Publication{
		Sections: []config.Section{sec("Atual", 3)},
		Paywall:  config.Paywall{PaidSections: []string{"Atual"}},
	}
	got := issueSections(iss, pub)
	if len(got) != 1 || got[0].Title != "Atual" {
		t.Fatalf("fallback to pub sections failed: %+v", got)
	}
	if got[0].Access != config.AccessSubscribers {
		t.Errorf("paid title should map to subscribers access, got %q", got[0].Access)
	}

	// a stored snapshot wins over the publication's current list —
	// renames after the run must not rewrite the edition
	iss.Sections = []config.Section{{Title: "Antiga", Items: 2, Commentary: "required"}}
	got = issueSections(iss, pub)
	if len(got) != 1 || got[0].Title != "Antiga" {
		t.Fatalf("snapshot should win: %+v", got)
	}
	if !sectionsChanged(iss, pub) {
		t.Error("sectionsChanged should flag drift between snapshot and builder")
	}
	iss2 := &store.Issue{Sections: []config.Section{sec("Atual", 3)}}
	if sectionsChanged(iss2, pub) {
		t.Error("identical snapshot should not flag drift")
	}
	if got := paidSectionTitles(iss2, pub); len(got) != 1 || got[0] != "Atual" {
		t.Errorf("paid titles from snapshot: %v", got)
	}
}
