package web

// Public pre-launch waitlist (KLOX-127). POST /api/v1/waitlist takes
// {email, source}; it needs no workspace (the visitor has no session),
// so ServeHTTP routes it around workspace resolution. The response is
// always the same {"ok":true} for a valid address — new or already
// listed — so the endpoint cannot be used to probe who signed up.

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	waitlistPerHour = 10 // per client IP
	waitlistMaxBody = 4 << 10
)

type waitlistLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *waitlistLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	now := time.Now()
	cutoff := now.Add(-time.Hour)
	keep := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= waitlistPerHour {
		l.hits[ip] = keep
		return false
	}
	l.hits[ip] = append(keep, now)
	if len(l.hits) > 4096 { // bound memory: drop idle IPs
		for k, ts := range l.hits {
			if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
				delete(l.hits, k)
			}
		}
	}
	return true
}

func waitlistClientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("Fly-Client-IP")); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) apiWaitlist(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		apiErr(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"mutating /api/v1 routes need Content-Type: application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, waitlistMaxBody)
	var in struct {
		Email  string `json:"email"`
		Source string `json:"source"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if len(email) > 254 || !validEmail(email) {
		apiErr(w, 400, "validation", "a plain email address is required")
		return
	}
	if !s.waitlistRL.allow(waitlistClientIP(r)) {
		apiErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests — try again later")
		return
	}
	source := strings.TrimSpace(in.Source)
	if len(source) > 64 {
		source = source[:64]
	}
	if s.d.Pool == nil {
		apiErr(w, 503, "unavailable", "waitlist is not available")
		return
	}
	if err := s.d.Pool.AddWaitlist(r.Context(), email, source); err != nil {
		apiErr(w, 500, "internal", "could not save your email — try again")
		return
	}
	apiJSON(w, 200, map[string]any{"ok": true})
}
