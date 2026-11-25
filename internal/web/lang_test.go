package web_test

// KLOX-232: the interface language persists — the magic link carries
// ?lang=, the confirm saves it on the account and the SPA picks it off
// the /start redirect; and the Radar theme field never shows the
// editorias' labels (they live as chips only).
import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// TestClaimMailCarriesLang checks both link and subject stamp: an EN
// mail gets "&lang=en" and a 12 h clock ("7:39 AM"), the PT mail keeps
// "&lang=pt" and "07h39" (KLOX-232).
func TestClaimMailCarriesLang(t *testing.T) {
	srv, db := emptyServer(t)
	ctx := context.Background()

	cases := []struct {
		lang     string
		wantLink string
		subject  *regexp.Regexp
	}{
		{"en", "&lang=en", regexp.MustCompile(`^Your Kloxus link — \d{1,2}:\d{2} (AM|PM)$`)},
		{"pt", "&lang=pt", regexp.MustCompile(`^Seu link do Kloxus — \d{1,2}h\d{2}$`)},
	}
	for _, tc := range cases {
		rr := apiCall(t, srv, "POST", "/api/v1/workspace/claim", map[string]string{"email": "o@x.test", "lang": tc.lang})
		if rr.Code != 200 {
			t.Fatalf("claim %s = %d %s", tc.lang, rr.Code, shortBody(rr))
		}
		mails, err := db.OutboxEmails(ctx)
		if err != nil {
			t.Fatalf("OutboxEmails: %v", err)
		}
		mail, err := db.GetMail(ctx, mails[len(mails)-1].ID)
		if err != nil {
			t.Fatalf("GetMail: %v", err)
		}
		if !strings.Contains(mail.BodyMD, tc.wantLink) {
			t.Fatalf("%s link lacks %q: %s", tc.lang, tc.wantLink, mail.BodyMD)
		}
		if !tc.subject.MatchString(mail.Subject) {
			t.Fatalf("%s subject = %q", tc.lang, mail.Subject)
		}
	}
}

// TestClaimConfirmSavesLang: the confirm lands on /start?lang= and the
// account keeps the language the link carried.
func TestClaimConfirmSavesLang(t *testing.T) {
	srv, db, _, _ := testServer(t, false, nil)
	ctx := context.Background()

	rr := apiCall(t, srv, "POST", "/api/v1/workspace/claim", map[string]string{"email": "o@x.test", "lang": "en"})
	if rr.Code != 200 {
		t.Fatalf("claim = %d %s", rr.Code, shortBody(rr))
	}
	tok := claimToken(t, db)
	rr = apiCall(t, srv, "GET", "/claim?t="+tok+"&lang=en", nil)
	if rr.Code != 200 {
		t.Fatalf("GET /claim = %d, want confirm page", rr.Code)
	}
	rr = apiCall(t, srv, "POST", "/claim?t="+tok+"&lang=en", nil)
	if rr.Code != 303 {
		t.Fatalf("POST /claim = %d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/start?lang=en" {
		t.Fatalf("redirect = %q, want /start?lang=en", loc)
	}
	if got := db.UILang(ctx); got != "en" {
		t.Fatalf("ui_lang = %q, want en", got)
	}
}

// TestWorkspaceLangRoundTrip: POST /api/v1/workspace/lang writes the
// account preference and GET /api/v1/workspace reports it back.
func TestWorkspaceLangRoundTrip(t *testing.T) {
	srv, _ := emptyServer(t)

	if got := apiGet(t, srv, "/api/v1/workspace")["lang"]; got != "" {
		t.Fatalf("fresh workspace lang = %q, want empty", got)
	}
	rr := apiCall(t, srv, "POST", "/api/v1/workspace/lang", map[string]string{"lang": "pt"})
	if rr.Code != 200 {
		t.Fatalf("save lang = %d %s", rr.Code, shortBody(rr))
	}
	if got := apiGet(t, srv, "/api/v1/workspace")["lang"]; got != "pt" {
		t.Fatalf("workspace lang = %q, want pt", got)
	}
	rr = apiCall(t, srv, "POST", "/api/v1/workspace/lang", map[string]string{"lang": "fr"})
	if rr.Code != 400 {
		t.Fatalf("bad lang = %d, want 400", rr.Code)
	}
}

// TestRadarTopicKeepsBeatsOutOfTheme: picked editorias show as chips
// alone — the theme field never carries their labels (KLOX-232). A
// theme save re-folds them into the stored topic, so the ranking keeps
// the signal and the chips stay.
func TestRadarTopicKeepsBeatsOutOfTheme(t *testing.T) {
	srv, _ := emptyServer(t)

	rr := apiCall(t, srv, "POST", "/api/v1/radar", map[string]any{
		"name":   "Brazil fintech watch",
		"about":  "Pix, open finance, Drex, central bank rules, digital banks",
		"niches": []string{"economia", "mercado-financeiro"},
	})
	if rr.Code != 201 {
		t.Fatalf("create radar = %d %s", rr.Code, shortBody(rr))
	}
	pub := apiJSON(t, rr)["publication"].(map[string]any)
	id := fmt.Sprint(int64(pub["id"].(float64)))

	topic, _ := pub["topic"].(string)
	for _, label := range []string{"economia", "mercado financeiro"} {
		if strings.Contains(strings.ToLower(topic), label) {
			t.Fatalf("theme field carries the %q label: %q", label, topic)
		}
	}
	niches, _ := pub["niches"].([]any)
	if len(niches) != 2 {
		t.Fatalf("niches = %v, want the two editorias", niches)
	}

	// editing the theme re-folds the beats: the field round-trips clean
	// and the chips never move
	rr = apiCall(t, srv, "PUT", "/api/v1/publications/"+id, map[string]any{"topic": "Pix, Drex"})
	if rr.Code != 200 {
		t.Fatalf("PUT topic = %d %s", rr.Code, shortBody(rr))
	}
	pub = apiJSON(t, rr)["publication"].(map[string]any)
	if got, _ := pub["topic"].(string); got != "Pix, Drex" {
		t.Fatalf("saved theme = %q, want Pix, Drex", got)
	}
	if n, _ := pub["niches"].([]any); len(n) != 2 {
		t.Fatalf("niches after save = %v, want the two editorias", n)
	}
}
