// Package examples embeds the demo-mode example data (publication
// definitions, fixture feeds and recorded LLM responses) so
// `kloxus demo` works from any directory.
package examples

import "embed"

//go:embed publications fixtures landing
var FS embed.FS
