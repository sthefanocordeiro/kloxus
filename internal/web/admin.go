package web

// Operator admin surface (D32/D36): the LLM kill switch and a live
// budget snapshot. Bearer-authenticated with KLOXUS_ADMIN_TOKEN — these
// routes bypass workspace resolution in ServeHTTP.
import (
	"crypto/subtle"
	"net/http"
)

// adminAuthed checks the bearer token against the configured admin
// token. An unset token disables the surface entirely.
func (s *Server) adminAuthed(w http.ResponseWriter, r *http.Request) bool {
	tok := s.d.Cfg.AdminToken
	if tok == "" {
		apiErr(w, http.StatusForbidden, "admin_disabled",
			"admin API is disabled — set KLOXUS_ADMIN_TOKEN to enable it")
		return false
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+tok)) != 1 {
		apiErr(w, http.StatusUnauthorized, "unauthorized", "invalid admin token")
		return false
	}
	return true
}

// adminLLMStatus reports the guard's live state: kill switch, today's
// recorded spend vs the global cap, and the configured budgets.
func (s *Server) adminLLMStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"kill":            s.d.Guard != nil && s.d.Guard.Killed(r.Context()),
		"mode":            s.d.Cfg.Mode,
		"runs_per_day":    s.d.Cfg.RunsPerDay,
		"tokens_per_run":  s.d.Cfg.TokensPerRun,
		"daily_cap_usd":   s.d.Cfg.LLMDailyCap,
		"spend_today_usd": 0.0,
		"provider":        "replay",
	}
	if s.d.Guard != nil {
		out["spend_today_usd"] = s.d.Guard.SpendToday(r.Context())
	}
	if s.d.Cfg.Mode == "real" {
		out["provider"] = "anthropic"
	}
	apiJSON(w, http.StatusOK, out)
}

func (s *Server) apiAdminLLM(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthed(w, r) {
		return
	}
	s.adminLLMStatus(w, r)
}

// apiAdminLLMSet flips the persisted kill switch: {"kill": true} stops
// every real model call immediately, {"kill": false} resumes. No
// restart needed — the next call sees the flag.
func (s *Server) apiAdminLLMSet(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthed(w, r) {
		return
	}
	var in struct {
		Kill *bool `json:"kill"`
	}
	if _, err := apiDecode(r, &in); err != nil || in.Kill == nil {
		apiErr(w, http.StatusBadRequest, "bad_json", "body must be JSON like {\"kill\": true}")
		return
	}
	if s.d.Guard == nil {
		apiErr(w, http.StatusConflict, "no_guard", "the spend guard is not configured on this server")
		return
	}
	if err := s.d.Guard.SetKill(r.Context(), *in.Kill); err != nil {
		apiDBErr(w, r, err, "kill switch")
		return
	}
	s.adminLLMStatus(w, r)
}
