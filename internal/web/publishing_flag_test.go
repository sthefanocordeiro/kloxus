package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
)

// KLOX-122: with KLOXUS_PUBLISHING off (default) every reader-facing
// route 404s before any workspace is resolved.
func TestPublishingOffHidesReaderSurface(t *testing.T) {
	srv := New(Deps{Cfg: &config.Config{Mode: "demo"}})
	for _, c := range [][2]string{
		{"GET", "/p/radar"}, {"GET", "/p/radar/3"}, {"GET", "/unsub"}, {"POST", "/unsub"},
		{"GET", "/confirm-sub"}, {"GET", "/outbox/1/html"}, {"POST", "/webhooks/abacatepay"},
		{"POST", "/api/v1/p/radar/subscribe"}, {"POST", "/api/v1/p/radar/checkout"},
		{"GET", "/api/v1/p/radar"}, {"GET", "/api/v1/publications/1/outbox"},
		{"GET", "/api/v1/publications/1/reader"}, {"POST", "/api/v1/issues/1/send"},
		{"POST", "/api/v1/publications/1/paywall/simulate"}, {"GET", "/publications/1/readers"},
	} {
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, httptest.NewRequest(c[0], c[1], nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c[0], c[1], rr.Code)
		}
	}
}

func TestIsPublishingPathKeepsEditorRoutes(t *testing.T) {
	for _, p := range []string{"/", "/publications/1", "/publications/1/sources", "/issues/2",
		"/api/v1/issues/2/approve", "/api/v1/publications/1/issues", "/claim", "/healthz"} {
		if isPublishingPath(httptest.NewRequest("GET", p, nil)) {
			t.Errorf("%s should stay available", p)
		}
	}
}
