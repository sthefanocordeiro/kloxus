package web

import (
	"errors"
	"net/http"

	"github.com/sthefanocordeiro/kloxus/internal/workspace"
)

func (s *Server) apiDemoReset(w http.ResponseWriter, r *http.Request) {
	if err := s.d.WS.Reset(r.Context(), reqWS(r)); err != nil {
		apiErr(w, 500, "internal", "reset failed")
		return
	}
	apiJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) apiDemoNew(w http.ResponseWriter, r *http.Request) {
	ws, err := s.d.WS.New(w, r)
	if err != nil {
		var rl *workspace.RateLimitedError
		if errors.As(err, &rl) {
			s.rateLimited(w, r, rl)
		} else {
			apiErr(w, 500, "internal", "internal error")
		}
		return
	}
	apiJSON(w, 200, map[string]any{"workspace": map[string]any{"id": ws.ID, "label": ws.Label}})
}
