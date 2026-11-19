// Package realsample carries the recorded real issue (D32): the
// publication definition and the captured feeds.
//
// The recorded LLM replies were removed in KLOX-46: they captured the
// pre-fix union-find dedupe and cannot replay under bounded clustering.
// `kloxus record --live` regenerates them post-merge (owner-only).
//
// The exported dossier payload and issue markdown were removed in
// KLOX-49: they contained dossiers built from outlets outside the
// catalog (g1), which the product is not allowed to show. The landing
// and blocked-run samples now always serve the fictional Valdória
// fixture (examples/landing/sample.json).
//
// Unlike everything under examples/, this content is REAL — real
// outlets, real stories. It lives outside the fictional fixture tree on
// purpose: TestFixturesAreFictional scans examples/ only.
package realsample

import (
	"embed"

	"github.com/sthefanocordeiro/kloxus/internal/config"
)

//go:embed pub.yaml
var defFS embed.FS

// FS holds the captured inputs that let `kloxus llm-check` replay the
// recorded run's feed side deterministically.
//
//go:embed all:feeds
var FS embed.FS

// Def parses the embedded real publication definition.
func Def() (*config.Publication, error) {
	data, err := defFS.ReadFile("pub.yaml")
	if err != nil {
		return nil, err
	}
	return config.ParsePublication(data)
}
