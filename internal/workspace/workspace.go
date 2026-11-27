// Package workspace isolates hosted visitors: each visitor gets a
// signed cookie and their own workspace row in the shared Postgres
// database (D28). A reset only ever touches the visitor's own
// workspace.
package workspace

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/strutil"
)

// CookieName is the workspace cookie sent to hosted visitors.
const CookieName = "kloxus_ws"

// Limits for the hosted app: workspaces outlive the evaluation window
// and creation is rate-limited per (IP, browser fingerprint) — CGNAT
// puts a whole office or carrier behind one address, so the key can't
// be the address alone (KLOX-193). There is no handle cache and no
// count-based eviction — idle workspaces are deleted by age only.
const (
	defaultMaxAge    = 21 * 24 * time.Hour
	createsPerHour   = 120
	touchInterval    = time.Hour
	cleanupInterval  = 10 * time.Minute
	cookieMaxAgeDays = 30
	entryIdle        = 24 * time.Hour
)

// ErrRateLimited is the sentinel Resolve and New wrap when the
// session-create limit refuses a mint — the concrete error is
// *RateLimitedError, which carries how long to wait.
var ErrRateLimited = errors.New("too many new sessions from this address — try again later")

// RateLimitedError reports a refused session mint. RetryAfter is the
// wait until the oldest create in the sliding window expires.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string { return ErrRateLimited.Error() }
func (e *RateLimitedError) Unwrap() error { return ErrRateLimited }

// Workspace is one visitor's environment: a workspace-scoped store
// handle plus coordination for pipeline runs and resets.
type Workspace struct {
	ID    string
	Label string
	DB    *store.DB
	// Activity is held for reading by pipeline runs and for writing by
	// resets, so a reset cannot wipe data mid-run.
	Activity sync.RWMutex
	// Runs tracks in-flight/finished pipeline runs by publication id.
	Runs sync.Map
}

// Manager resolves, creates and reclaims workspaces against the shared
// pool.
type Manager struct {
	pool   *store.Pool
	secret []byte // cookie HMAC key
	seed   func(ctx context.Context, db *store.DB) error
	local  *Workspace // non-nil in single-workspace (local) mode

	maxAge time.Duration

	mu       sync.Mutex
	entries  map[string]*Workspace // shared per-id coordination objects
	lastUse  map[string]time.Time
	lastSeen map[string]time.Time // last last_seen_at write per id

	createMu sync.Mutex
	creates  map[string][]time.Time
	// createLimit is the per-(IP,fingerprint) new-session cap per hour;
	// 0 means the createsPerHour default.
	createLimit int
}

// SetCreateLimit overrides the per-visitor new-session cap per hour
// (KLOXUS_NEW_SESSIONS_PER_IP_HOUR). Values <= 0 keep the current value.
func (m *Manager) SetCreateLimit(n int) {
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if n > 0 {
		m.createLimit = n
	}
}

var idRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NewID returns a random 128-bit workspace id (hex). crypto/rand never
// fails on supported platforms — since Go 1.24, Read is documented to
// always fill the buffer — so there is no error to propagate.
func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// OpenHosted creates a hosted manager: workspaces are rows in the
// shared database reached through pool. secret signs cookies — callers
// persist it themselves when they want sessions to survive restarts
// (see LoadOrCreateSecret).
func OpenHosted(ctx context.Context, pool *store.Pool, secret []byte, seed func(context.Context, *store.DB) error) (*Manager, error) {
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
	}
	m := &Manager{
		pool: pool, secret: secret, seed: seed,
		maxAge:   defaultMaxAge,
		entries:  map[string]*Workspace{},
		lastUse:  map[string]time.Time{},
		lastSeen: map[string]time.Time{},
		creates:  map[string][]time.Time{},
	}
	go m.cleanupLoop(ctx)
	return m, nil
}

// OpenLocal wraps a single scoped store as a fixed workspace for local
// runs (no cookies, one shared demo).
func OpenLocal(db *store.DB, seed func(context.Context, *store.DB) error) *Manager {
	return &Manager{
		local: &Workspace{ID: "local", Label: "local", DB: db},
		seed:  seed,
	}
}

// LoadOrCreateSecret reads a hex 32-byte secret from path, generating
// and persisting one when the file is missing or unreadable. Used to
// keep session cookies valid across deploys on the Fly volume.
func LoadOrCreateSecret(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if s, err := hex.DecodeString(strings.TrimSpace(string(data))); err == nil && len(s) == 32 {
			return s, nil
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(b)), 0o600); err != nil {
		return nil, err
	}
	return b, nil
}

// Hosted reports whether the manager serves per-visitor workspaces.
func (m *Manager) Hosted() bool { return m.local == nil }

// Local returns the fixed workspace in local mode.
func (m *Manager) Local() *Workspace { return m.local }

// Pool exposes the shared pool for global jobs (the scheduler).
func (m *Manager) Pool() *store.Pool { return m.pool }

// Healthy reports whether the database answers.
func (m *Manager) Healthy() bool {
	if m.local != nil {
		return m.local.DB.Ping() == nil
	}
	return m.pool.Ping() == nil
}

// MAC returns a scoped HMAC token for value (unsubscribe links, etc.).
func (m *Manager) MAC(scope, value string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(scope + ":" + value))
	return hex.EncodeToString(h.Sum(nil))[:24]
}

// ValidMAC reports whether tok is the MAC for (scope, value).
func (m *Manager) ValidMAC(scope, value, tok string) bool {
	return subtle.ConstantTimeCompare([]byte(m.MAC(scope, value)), []byte(tok)) == 1
}

// PublicToken wraps a workspace id in an opaque, tamper-proof token for
// reader-facing links — the workspace id never appears in a public URL.
// AES-256-GCM authenticated encryption with a random nonce.
func (m *Manager) PublicToken(wsID string) string {
	key := sha256.Sum256(m.secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(wsID), nil))
}

// ResolvePublicToken unwraps a PublicToken back to the workspace id, or
// "" when the token is malformed or forged.
func (m *Manager) ResolvePublicToken(tok string) string {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return ""
	}
	key := sha256.Sum256(m.secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	if len(raw) < gcm.NonceSize()+gcm.Overhead() {
		return ""
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

// signLegacy is the pre-epoch cookie signature. Cookies already in
// browsers keep this shape valid as epoch 0 until the workspace's first
// logout rotation (KLOX-279).
func (m *Manager) signLegacy(id string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(id))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// signEpoch signs the versioned workspace credential. Binding the epoch
// into the MAC means changing the version in a copied cookie is a forgery,
// not a downgrade or upgrade.
func (m *Manager) signEpoch(id string, epoch int64) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte("workspace-cookie:" + id + ":" + strconv.FormatInt(epoch, 10)))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// cookieCredential extracts and verifies the workspace id and credential
// epoch from the request cookie. The legacy two-field shape (id.signature)
// is epoch 0; the current shape is id.epoch.signature.
func (m *Manager) cookieCredential(r *http.Request) (id string, epoch int64, ok bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return "", 0, false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) == 2 {
		id, sig := parts[0], parts[1]
		if !idRE.MatchString(id) || subtle.ConstantTimeCompare([]byte(sig), []byte(m.signLegacy(id))) != 1 {
			return "", 0, false
		}
		return id, 0, true
	}
	if len(parts) != 3 {
		return "", 0, false
	}
	id, epochText, sig := parts[0], parts[1], parts[2]
	epoch, err = strconv.ParseInt(epochText, 10, 64)
	if !idRE.MatchString(id) || err != nil || epoch < 0 || strconv.FormatInt(epoch, 10) != epochText {
		return "", 0, false
	}
	if subtle.ConstantTimeCompare([]byte(sig), []byte(m.signEpoch(id, epoch))) != 1 {
		return "", 0, false
	}
	return id, epoch, true
}

// cookieID extracts and verifies a workspace id from the request cookie.
func (m *Manager) cookieID(r *http.Request) string {
	id, _, ok := m.cookieCredential(r)
	if !ok {
		return ""
	}
	return id
}

// ExistingWorkspaceID validates the request's workspace cookie without
// minting, touching or reseeding anything. Public pages use it to tell a
// returning browser apart from a first visit; a missing, forged, deleted
// or logged-out credential is simply inactive.
func (m *Manager) ExistingWorkspaceID(ctx context.Context, r *http.Request) (string, bool, error) {
	if m.local != nil {
		// Local mode has one implicit workspace, not a per-browser session.
		return "", false, nil
	}
	id, epoch, valid := m.cookieCredential(r)
	if !valid {
		return "", false, nil
	}
	if m.pool == nil {
		return "", false, fmt.Errorf("workspace credential store unavailable")
	}
	current, exists, err := m.pool.WorkspaceAuthEpoch(ctx, id)
	if err != nil {
		return "", false, err
	}
	if !exists || current != epoch {
		return "", false, nil
	}
	return id, true, nil
}

// SetCookie writes the signed workspace cookie at the workspace's current
// persisted auth epoch. Reading the epoch at mint time keeps claim/login
// recovery aligned with the newest logout rotation.
func (m *Manager) SetCookie(w http.ResponseWriter, r *http.Request, id string) error {
	epoch := int64(0)
	if m.pool != nil {
		current, ok, err := m.pool.WorkspaceAuthEpoch(r.Context(), id)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("workspace not found")
		}
		epoch = current
	}
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	epochText := strconv.FormatInt(epoch, 10)
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: id + "." + epochText + "." + m.signEpoch(id, epoch),
		Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: cookieMaxAgeDays * 24 * 3600,
	})
	return nil
}

// ClearCookie expires the workspace cookie: the browser is anonymous
// again and the next visit starts a fresh workspace. The workspace
// itself is untouched — a claimed account signs back in by email link.
func (m *Manager) ClearCookie(w http.ResponseWriter, r *http.Request) {
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// clientIP extracts the client address for rate limiting. On Fly the
// platform-set Fly-Client-IP header wins — X-Forwarded-For is
// client-controlled and must not drive the limit (review F3).
func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("Fly-Client-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// createKey is the rate-limit bucket for session minting: the client IP
// plus a short fingerprint of what the browser says about itself. A
// tracking cookie would be sturdier, but the two headers every real
// browser already sends separate the users sharing one CGNAT address
// (KLOX-193) — no new state, no new cookie.
func createKey(r *http.Request) string {
	sum := sha256.Sum256([]byte(r.UserAgent() + "|" + r.Header.Get("Accept-Language")))
	return clientIP(r) + "|" + hex.EncodeToString(sum[:4])
}

// allowCreate rate-limits workspace creation per (IP, fingerprint)
// pair (sliding window) and reports the wait when the window is full.
// Idle entries are pruned so the map stays bounded (review F3).
func (m *Manager) allowCreate(key string) (bool, time.Duration) {
	m.createMu.Lock()
	defer m.createMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Hour)
	keep := m.creates[key][:0]
	for _, t := range m.creates[key] {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	limit := createsPerHour
	if m.createLimit > 0 {
		limit = m.createLimit
	}
	if len(keep) >= limit {
		m.creates[key] = keep
		return false, keep[0].Add(time.Hour).Sub(now)
	}
	m.creates[key] = append(keep, now)
	if len(m.creates) > 4096 {
		for k, ts := range m.creates {
			fresh := ts[:0]
			for _, t := range ts {
				if t.After(cutoff) {
					fresh = append(fresh, t)
				}
			}
			if len(fresh) == 0 {
				delete(m.creates, k)
			} else {
				m.creates[k] = fresh
			}
		}
	}
	return true, 0
}

// Resolve returns the workspace for a request: the cookie's workspace in
// hosted mode (creating one and setting the cookie when absent), or the
// fixed local workspace.
func (m *Manager) Resolve(w http.ResponseWriter, r *http.Request) (*Workspace, error) {
	if m.local != nil {
		return m.local, nil
	}
	if id, epoch, valid := m.cookieCredential(r); valid {
		if m.pool == nil {
			return nil, fmt.Errorf("workspace credential store unavailable")
		}
		current, exists, err := m.pool.WorkspaceAuthEpoch(r.Context(), id)
		if err != nil {
			return nil, err
		}
		if exists && current == epoch {
			if ws, err := m.Get(r.Context(), id); err == nil {
				return ws, nil
			}
		}
		// Unknown/deleted workspace or a credential retired by logout:
		// fall through and create a fresh empty session, as before.
	}
	if ok, retry := m.allowCreate(createKey(r)); !ok {
		return nil, &RateLimitedError{RetryAfter: retry}
	}
	ws, err := m.Create(r.Context())
	if err != nil {
		return nil, err
	}
	if err := m.SetCookie(w, r, ws.ID); err != nil {
		return nil, err
	}
	return ws, nil
}

// New abandons the visitor's current workspace and starts a fresh one.
func (m *Manager) New(w http.ResponseWriter, r *http.Request) (*Workspace, error) {
	if m.local != nil {
		return m.local, nil
	}
	if ok, retry := m.allowCreate(createKey(r)); !ok {
		return nil, &RateLimitedError{RetryAfter: retry}
	}
	ws, err := m.Create(r.Context())
	if err != nil {
		return nil, err
	}
	if err := m.SetCookie(w, r, ws.ID); err != nil {
		return nil, err
	}
	return ws, nil
}

// Get returns the workspace for an id — the shared coordination object
// so runs, resets and the activity lock agree across requests. Ids are
// validated before any lookup (review F1).
func (m *Manager) Get(ctx context.Context, id string) (*Workspace, error) {
	if m.local != nil {
		if id == m.local.ID {
			return m.local, nil
		}
		return nil, fmt.Errorf("unknown workspace %q", id)
	}
	if !idRE.MatchString(id) {
		return nil, fmt.Errorf("invalid workspace id %q", id)
	}
	// Entries are created only after WorkspaceExists verifies the row,
	// so a cached entry already proves existence — skip the per-request
	// lookup (one remote-DB round-trip on every API call, KLOX-47). The
	// hourly touch keeps last_seen_at fresh, so the idle sweeper cannot
	// reap the workspace out from under a held entry.
	m.mu.Lock()
	_, known := m.entries[id]
	m.mu.Unlock()
	if !known {
		if ok, err := m.pool.WorkspaceExists(ctx, id); err != nil {
			return nil, err
		} else if !ok {
			return nil, fmt.Errorf("workspace %s not found", id)
		}
	}
	ws := m.entry(id)
	// Refresh canonical publication config from the current template so
	// sessions created before a deploy pick up fixture changes (price,
	// sections…). Only workspaces with a template-seeded (non-custom)
	// publication get the refresh — creator-made publications keep their
	// edited blocks, and empty workspaces stay empty.
	if m.seed != nil {
		if has, herr := ws.DB.HasCanonicalPublication(ctx); herr != nil {
			slog.Warn("workspace reseed check failed", "ws", id, "err", herr)
		} else if has {
			if err := m.seed(ctx, ws.DB); err != nil {
				slog.Warn("workspace reseed failed", "ws", id, "err", err)
			}
		}
	}
	m.touch(ctx, id)
	return ws, nil
}

// entry returns the shared Workspace for id, creating it on first use.
func (m *Manager) entry(id string) *Workspace {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ws, ok := m.entries[id]; ok {
		m.lastUse[id] = time.Now()
		return ws
	}
	ws := &Workspace{ID: id, Label: labelOf(id), DB: m.pool.For(id)}
	m.entries[id] = ws
	m.lastUse[id] = time.Now()
	return ws
}

// Create inserts a workspace row and returns the new workspace.
func (m *Manager) Create(ctx context.Context) (*Workspace, error) {
	id := NewID()
	if err := m.pool.CreateWorkspace(ctx, id); err != nil {
		return nil, err
	}
	return m.Get(ctx, id)
}

// Reset wipes one workspace's data, waiting for in-flight pipeline runs
// to finish. The workspace returns to the empty state the visitor had at
// onboarding — nothing is reseeded, so they walk the create flow again.
func (m *Manager) Reset(ctx context.Context, ws *Workspace) error {
	ws.Activity.Lock()
	defer ws.Activity.Unlock()
	if err := ws.DB.Reset(ctx); err != nil {
		return err
	}
	ws.Runs.Range(func(k, _ any) bool {
		ws.Runs.Delete(k)
		return true
	})
	return nil
}

// touch refreshes the workspace row's last_seen_at (throttled) so the
// cleanup sweep keeps workspaces that are in use. The caller's ctx is
// capped at 30s so a dead pooled connection can't park the caller's
// goroutine.
func (m *Manager) touch(ctx context.Context, id string) {
	m.mu.Lock()
	last, seen := m.lastSeen[id]
	if seen && time.Since(last) < touchInterval {
		m.mu.Unlock()
		return
	}
	m.lastSeen[id] = time.Now()
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := m.pool.TouchWorkspace(ctx, id); err != nil {
		slog.Warn("workspace touch failed", "ws", id, "err", err)
	}
}

// CloseWorkspace drops the shared coordination object for an id; the
// workspace row itself is untouched.
func (m *Manager) CloseWorkspace(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, id)
	delete(m.lastUse, id)
	delete(m.lastSeen, id)
}

// Close releases the pool (local mode: nothing else to release).
func (m *Manager) Close() {
	if m.local != nil {
		m.local.DB.Close()
		return
	}
	m.pool.Close()
}

// cleanupLoop deletes abandoned workspaces and stale coordination
// entries on a schedule — the Postgres replacement for the per-file
// sweeper (D28).
func (m *Manager) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Bound one sweep — a dead conn in DeleteIdleWorkspaces
			// would otherwise wedge the cleanup loop for good.
			cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			m.cleanup(cctx)
			cancel()
		}
	}
}

func (m *Manager) cleanup(ctx context.Context) {
	cutoff := time.Now().Add(-m.maxAge)
	if n, err := m.pool.DeleteIdleWorkspaces(ctx, store.TSUTC(cutoff)); err != nil {
		slog.Warn("workspace cleanup failed", "err", err)
	} else if n > 0 {
		slog.Info("deleted idle workspaces", "count", n)
	}
	// Sweep coordination objects for ids nobody has resolved in a day —
	// deleted workspaces' entries die here too.
	m.mu.Lock()
	for id, t := range m.lastUse {
		if time.Since(t) > entryIdle {
			delete(m.entries, id)
			delete(m.lastUse, id)
			delete(m.lastSeen, id)
		}
	}
	m.mu.Unlock()
}

// labelOf names a hosted workspace for API payloads. It is not a
// person: user-facing surfaces (the approval receipt) use the claimed
// owner e-mail instead, never this label (§2 never-show, KLOX-64).
func labelOf(id string) string {
	return "visitor " + strutil.Truncate(id, 4)
}
