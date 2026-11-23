package web

// Newsletter template endpoints (KLOX-72): the publication's default
// edition template — an ordered block list (the composer's kinds plus
// "section" slots that materialize the week's pautas). Edition
// creation copies it into the stored body; the composer then edits
// the instance only. Saves carry a version like the document's —
// stale writes conflict instead of clobbering a concurrent edit.
import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// templateMax bounds the stored block list — a newsletter layout never
// needs more than a few dozen entries.
const templateMax = 100

// templateVersion is the template's optimistic-concurrency token — a
// hash of the stored template_json, shaped like the document version.
func templateVersion(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

// effectiveTemplate is what the editor edits: the stored block list,
// or — when none was saved yet — one section slot per section in
// order, which materializes exactly like the legacy layout.
func effectiveTemplate(pub *store.Publication) []store.TemplateBlock {
	if len(pub.Template) > 0 {
		return pub.Template
	}
	out := make([]store.TemplateBlock, 0, len(pub.Sections))
	for _, sc := range pub.Sections {
		out = append(out, store.TemplateBlock{Kind: "section", Section: sc.Title})
	}
	return out
}

// apiTemplateGet answers the newsletter's editable template.
func (s *Server) apiTemplateGet(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	apiJSON(w, 200, map[string]any{"template": map[string]any{
		"blocks":  effectiveTemplate(pub),
		"version": templateVersion(pub.TemplateRaw),
	}})
}

// validateTemplateBlock checks one template entry: a section slot
// needs a title; every fixed kind must render a valid stored line —
// the same rule the composer insert path applies. Embed text is
// stored sanitized, matching the issue body's write-time boundary.
func validateTemplateBlock(b *store.TemplateBlock) bool {
	if b.Kind == "section" {
		b.Section = strings.TrimSpace(b.Section)
		return b.Section != "" && len(b.Section) <= 120
	}
	if _, ok := delivery.BlockLine(b.Kind, b.Text, b.URL); !ok {
		return false
	}
	if b.Kind == "embed" {
		b.Text = delivery.SanitizeEmbed(b.Text)
	}
	return true
}

// apiTemplateSave replaces the newsletter's template. The change lands
// on editions created after the save — in-progress editions keep the
// blocks their body already materialized (KLOX-72).
func (s *Server) apiTemplateSave(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	var in struct {
		Blocks  []store.TemplateBlock `json:"blocks"`
		Version string                `json:"version"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	if in.Blocks == nil {
		in.Blocks = []store.TemplateBlock{}
	}
	if len(in.Blocks) > templateMax {
		apiErr(w, 400, "validation", "the template is too long (100 blocks max)")
		return
	}
	for i := range in.Blocks {
		if !validateTemplateBlock(&in.Blocks[i]) {
			apiErr(w, 400, "validation",
				"each template block needs a valid kind (section|heading|divider|link|image|embed|text) and content")
			return
		}
	}
	if in.Version == "" || in.Version != templateVersion(pub.TemplateRaw) {
		apiErr(w, 409, "conflict",
			"the template changed since this version — reload and retry the edit")
		return
	}
	err = db(r).SetPublicationTemplate(r.Context(), pub.ID, pub.TemplateRaw, in.Blocks)
	if err == sql.ErrNoRows {
		apiErr(w, 409, "conflict",
			"the template changed since this version — reload and retry the edit")
		return
	}
	if apiDBErr(w, r, err, "publication") {
		return
	}
	raw, _ := json.Marshal(in.Blocks)
	apiJSON(w, 200, map[string]any{"template": map[string]any{
		"blocks":  in.Blocks,
		"version": templateVersion(string(raw)),
	}})
}
