package web

// The fixture article page behind every source link, so "every fact has
// a working source reference" is visibly true: citations in an issue
// open /a/{id} and read the fictional original.
import (
	"database/sql"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/sthefanocordeiro/kloxus/internal/dedupe"
)

func (s *Server) articlePage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	it, err := db(r).GetItem(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "article not found", http.StatusNotFound)
			return
		}
		slog.Error("article page", "err", err, "rid", reqIDOf(r))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	meta := it.PublishedAt
	if it.Author != "" {
		meta += " · " + it.Author
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>%s — %s</title>`+
		`<style>body{font-family:Georgia,serif;max-width:38em;margin:3em auto;padding:0 1.2em;color:#1c1c1c;line-height:1.6}`+
		`.site{color:#0e7a6d;font-size:.85em;text-transform:uppercase;letter-spacing:.08em}`+
		`.muted{color:#666;font-size:.85em}.url{word-break:break-all}`+
		`.note{margin-top:3em;padding:.9em 1.1em;border:1px dashed #bbb;border-radius:.5em;color:#666;font-size:.85em;font-family:system-ui,sans-serif}`+
		`</style></head><body>`+
		`<p class="site">%s</p>`+
		`<p class="muted">Simulated article — original URL (fictional): <span class="url">%s</span></p>`+
		`<h1>%s</h1><p class="muted">%s</p><p>%s</p>`+
		`<div class="note">Conteúdo simulado · fictional source · <a href="/">← Kloxus</a></div>`+
		`</body></html>`,
		html.EscapeString(it.Title), html.EscapeString(it.SourceName),
		html.EscapeString(it.SourceName), html.EscapeString(dedupe.Canonical(it.URL)),
		html.EscapeString(it.Title), html.EscapeString(meta),
		html.EscapeString(it.ContentText))
}
