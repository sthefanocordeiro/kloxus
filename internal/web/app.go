package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// The React app (frontend/, built by Vite into internal/web/appdist) is
// embedded into the binary — no Node server, no CDN. appdist is
// gitignored except for a placeholder so a clean checkout still builds;
// until `npm run build` fills it, /app answers with a short note.
//
//go:embed all:appdist
var appFS embed.FS

func appDist() fs.FS {
	sub, err := fs.Sub(appFS, "appdist")
	if err != nil {
		return nil
	}
	return sub
}

// appRedirect sends /app to /app/ — the SPA's router base.
func (s *Server) appRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/app/", http.StatusMovedPermanently)
}

// appPage serves the SPA: real files get served (hashed assets
// immutable), everything else falls back to index.html so client-side
// routes like /app/issues/3 work on refresh.
func (s *Server) appPage(w http.ResponseWriter, r *http.Request) {
	dist := appDist()
	if dist == nil {
		http.Error(w, "frontend unavailable", http.StatusServiceUnavailable)
		return
	}
	p := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/app"))[1:]
	if p != "" && p != "index.html" {
		if st, err := fs.Stat(dist, p); err == nil && !st.IsDir() {
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.StripPrefix("/app/", http.FileServerFS(dist)).ServeHTTP(w, r)
			return
		}
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("frontend not built — run `npm run build` in frontend/\n"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}
