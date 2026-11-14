package delivery

// Loopback transport boundary tests (KLOX-32 remediation): the
// verification sender must refuse anything that is not a literal
// loopback endpoint — and every rejection must produce no sender at
// all, so zero outbound requests are possible from a bad config.
import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLoopbackSenderEndpointValidation(t *testing.T) {
	bad := []string{
		"https://api.resend.com",              // the real provider
		"http://example.com",                  // remote host
		"http://10.0.0.5:8399",                // private but not loopback
		"http://192.168.1.1:8399",             // private but not loopback
		"http://localhost:8399",               // DNS name — not a literal
		"https://127.0.0.1:8399",              // wrong scheme
		"http://user:pass@127.0.0.1:8399",     // userinfo — credential smuggle
		"http://127.0.0.1:8399/?next=evil",    // query
		"http://127.0.0.1:8399/#frag",         // fragment
		"http://127.0.0.1:8399/emails",        // path
		"127.0.0.1:8399",                      // no scheme
		"",                                    // empty
		"not a url",                           // garbage
		"http://[::ffff:8.8.8.8]:8399",        // non-loopback IPv4-in-IPv6
	}
	for _, ep := range bad {
		if rs, err := NewLoopbackSender(ep, "Kloxus <n@x.com>"); err == nil || rs != nil {
			t.Errorf("NewLoopbackSender(%q) = %v, %v — want nil, error", ep, rs, err)
		}
	}
	good := []string{
		"http://127.0.0.1:8399",
		"http://127.0.0.1:8399/",
		"http://127.0.0.55:1",   // any 127/8 literal
		"http://[::1]:8399",
	}
	for _, ep := range good {
		rs, err := NewLoopbackSender(ep, "Kloxus <n@x.com>")
		if err != nil || rs == nil {
			t.Errorf("NewLoopbackSender(%q) = %v, %v — want a sender", ep, rs, err)
			continue
		}
		if !rs.Synthetic || rs.Transport() != "synthetic" {
			t.Errorf("NewLoopbackSender(%q): Synthetic=%v Transport=%q", ep, rs.Synthetic, rs.Transport())
		}
	}
}

// TestLoopbackRejectMakesZeroRequests proves an invalid endpoint fails
// at construction: a listener on the very address it names sees no
// connection because no client is ever built.
func TestLoopbackRejectMakesZeroRequests(t *testing.T) {
	var dials atomic.Int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	for _, ep := range []string{
		fmt.Sprintf("http://user:pw@127.0.0.1:%d", port),
		fmt.Sprintf("http://127.0.0.1:%d/?q=1", port),
		fmt.Sprintf("http://127.0.0.1:%d/path", port),
	} {
		if _, err := NewLoopbackSender(ep, "Kloxus <n@x.com>"); err == nil {
			t.Errorf("NewLoopbackSender(%q) accepted an invalid endpoint", ep)
		}
	}
	if n := dials.Load(); n != 0 {
		t.Fatalf("rejected endpoints still made %d outbound connection(s)", n)
	}
}

// TestLoopbackSenderRefusesRedirects proves the fake cannot bounce the
// sender at a second destination: the redirect target sees zero
// requests and the send fails.
func TestLoopbackSenderRefusesRedirects(t *testing.T) {
	var target atomic.Int64
	bounce := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		target.Add(1)
	}))
	defer bounce.Close()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, bounce.URL+"/emails", http.StatusFound)
	}))
	defer fake.Close()
	rs, err := NewLoopbackSender(fake.URL, "Kloxus <n@x.com>")
	if err != nil {
		t.Fatalf("NewLoopbackSender: %v", err)
	}
	rs.MaxAttempts = 1
	if _, err := rs.Send(context.Background(), testMsg()); err == nil {
		t.Fatal("a redirecting fake must fail the send, not be followed")
	}
	if n := target.Load(); n != 0 {
		t.Fatalf("redirect target received %d request(s) — transport must not follow", n)
	}
}

// TestLoopbackSenderSendsSyntheticKey proves the verification transport
// never presents a real credential: the only key on the wire is the
// fixed synthetic one.
func TestLoopbackSenderSendsSyntheticKey(t *testing.T) {
	rs, calls := fakeResend(t, func(_ int, w http.ResponseWriter, _ *resendCall) { resendOK(w) })
	if _, err := rs.Send(context.Background(), testMsg()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := calls()
	if len(got) != 1 {
		t.Fatalf("calls = %d, want 1", len(got))
	}
	if got[0].auth != "Bearer "+syntheticKey {
		t.Fatalf("Authorization = %q, want the synthetic key", got[0].auth)
	}
	if rs.Transport() != "synthetic" {
		t.Fatalf("Transport = %q, want synthetic", rs.Transport())
	}
}

// TestRealSenderPinnedAndIdentified proves the production transport is
// pinned to the real API (the SDK's RESEND_BASE_URL env default cannot
// redirect it) and reports the "resend" identity.
func TestRealSenderPinnedAndIdentified(t *testing.T) {
	rs := NewResend("re_test_key", "Kloxus <n@x.com>")
	if got := rs.Client.BaseURL.String(); got != resendAPI {
		t.Fatalf("BaseURL = %q, want pinned %q", got, resendAPI)
	}
	if rs.Synthetic || rs.Transport() != "resend" {
		t.Fatalf("Transport = %q Synthetic=%v, want resend", rs.Transport(), rs.Synthetic)
	}
}
