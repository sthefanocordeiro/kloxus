package pipeline

import (
	"strings"
	"testing"
)

// The approve-time citecheck passes items as maps; keys must be the
// capitalized field names the template reads, or sources render as
// <no value> and the model never sees the source text.
func TestCitecheckApproveRendersSourceExcerpt(t *testing.T) {
	type sent struct {
		ID   int
		Item int
		Refs string
		Text string
	}
	out, err := RenderPrompt("citecheck_approve", map[string]any{
		"Publication": "Pub",
		"Sentences":   []sent{{ID: 1, Item: 1, Refs: "1", Text: "Claim."}},
		"Items": []map[string]any{{"Ref": 1, "Sources": []map[string]any{
			{"Ref": 1, "URL": "http://u.example/a", "Excerpt": "SRCTEXT"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SRCTEXT", "http://u.example/a", "item 1:", `answer "n/a"`} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<no value>") {
		t.Errorf("prompt has <no value>:\n%s", out)
	}
}
