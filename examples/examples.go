// Package examples embeds the demo-mode example data (publication
// definitions and, from milestone 2 on, fixture feeds and recorded
// LLM responses) so `kloxus demo` works from any directory.
package examples

import "embed"

//go:embed publications
var FS embed.FS
