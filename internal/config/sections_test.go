package config

import "testing"

func TestNormalizeSectionsFillsIDFormatAccess(t *testing.T) {
	in := []Section{
		{Title: "O destaque", Items: 1, Commentary: "required"},
		{Title: "Links rápidos", Items: 5, Commentary: "none"},
		{Title: "Vale saber", Items: 4, Commentary: "optional"},
	}
	out := NormalizeSections(in, []string{"Vale saber"})
	if len(out) != 3 {
		t.Fatalf("want 3 sections, got %d", len(out))
	}
	for i, s := range out {
		if s.ID == "" {
			t.Errorf("section %d: id not filled", i)
		}
		if !ValidSectionFormat(s.Format) {
			t.Errorf("section %d: format %q invalid", i, s.Format)
		}
		if !ValidSectionAccess(s.Access) {
			t.Errorf("section %d: access %q invalid", i, s.Access)
		}
	}
	if out[0].Format != SectionFeatured {
		t.Errorf("featured title: format %q, want featured", out[0].Format)
	}
	if out[1].Format != SectionLinks {
		t.Errorf("links title: format %q, want links", out[1].Format)
	}
	if out[2].Format != SectionNews {
		t.Errorf("news title: format %q, want news", out[2].Format)
	}
	if out[2].Access != AccessSubscribers {
		t.Errorf("paid section: access %q, want subscribers", out[2].Access)
	}
	if out[0].Access != AccessAll {
		t.Errorf("free section: access %q, want all", out[0].Access)
	}
}

func TestNormalizeSectionsKeepsExistingFields(t *testing.T) {
	in := []Section{{ID: "sec-9", Title: "X", Format: SectionLinks, Items: 2, Commentary: "none", Access: AccessSubscribers}}
	out := NormalizeSections(in, nil)
	if out[0].ID != "sec-9" || out[0].Format != SectionLinks || out[0].Access != AccessSubscribers {
		t.Errorf("existing fields overwritten: %+v", out[0])
	}
}

func TestPaidSectionTitles(t *testing.T) {
	secs := []Section{
		{Title: "A", Access: AccessAll},
		{Title: "B", Access: AccessSubscribers},
		{Title: "C", Access: AccessSubscribers},
	}
	got := PaidSectionTitles(secs)
	if len(got) != 2 || got[0] != "B" || got[1] != "C" {
		t.Errorf("paid titles %v, want [B C]", got)
	}
}
