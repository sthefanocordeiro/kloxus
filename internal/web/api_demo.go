package web

import (
	"net/http"
)

// --- demo workspace ---

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
		apiErr(w, 429, "rate_limited", err.Error())
		return
	}
	apiJSON(w, 200, map[string]any{"workspace": map[string]any{"id": ws.ID, "label": ws.Label}})
}
