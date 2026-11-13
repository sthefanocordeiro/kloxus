// Package realsample carries the recorded real issue (D32): the
// publication definition, the captured feeds and recorded LLM
// responses, and the exported sample dossier + issue markdown.
//
// Unlike everything under examples/, this content is REAL — real
// outlets, real stories. It lives outside the fictional fixture tree on
// purpose: TestFixturesAreFictional scans examples/ only. The web layer
// serves Sample() when a run is blocked (budget, cap, kill switch) and
// for the landing page's cited sample in real mode.
package realsample

import (
	"embed"

	"github.com/sthefanocordeiro/kloxus/internal/config"
)

//go:embed pub.yaml
var defFS embed.FS

// feedsFS and llmFS hold the captured inputs that let `kloxus llm-check`
// replay the recorded run's feed side deterministically.
//
//go:embed all:feeds all:llm sample.json issue.md
var FS embed.FS

// Def parses the embedded real publication definition.
func Def() (*config.Publication, error) {
	data, err := defFS.ReadFile("pub.yaml")
	if err != nil {
		return nil, err
	}
	return config.ParsePublication(data)
}

// Sample returns the exported dossier payload served by /api/v1/sample,
// or nil when the build carries no exported sample yet.
func Sample() []byte {
	data, err := FS.ReadFile("sample.json")
	if err != nil {
		return nil
	}
	return data
}
