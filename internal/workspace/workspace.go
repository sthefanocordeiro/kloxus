// Package workspace isolates hosted-demo visitors: each visitor gets a
// signed cookie and their own SQLite database copied from a seeded
// template. A reset only ever touches the visitor's own workspace.
package workspace

import (
	"container/list"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// CookieName is the workspace cookie sent to hosted-demo visitors.
const CookieName = "kloxus_ws"

// Limits for the hosted demo: workspaces outlive the evaluation window,
// handle count stays bounded, and creation is rate-limited per IP.
const (
	defaultMaxOpen   = 64
	defaultMaxCount  = 2000
	defaultMaxAge    = 21 * 24 * time.Hour
	createsPerHour   = 30
	touchInterval    = time.Hour
	cleanupInterval  = 10 * time.Minute
	cookieMaxAgeDays = 30
)

// Workspace is one visitor's demo environment: a private database plus
// coordination for pipeline runs and resets.
type Workspace struct {
	ID    string
	Label string
	Dir   string
	DB    *store.DB
	// Activity is held for reading by pipeline runs and for writing by
	// resets, so a reset cannot wipe data mid-run.
	Activity sync.RWMutex
	// Runs tracks in-flight/finished pipeline runs by publication id.
	Runs sync.Map
	// RunMu serializes pipeline starts: one run at a time per workspace.
	RunMu sync.Mutex
}

// Manager resolves, creates, caches and reclaims workspaces.
type Manager struct {
	dir      string // workspaces dir
	template string // seeded template db copied on workspace creation
	secret   []byte // cookie HMAC key
	seed     func(ctx context.Context, db *store.DB) error
	local    *Workspace // non-nil in single-workspace (local) mode

	maxOpen  int
	maxCount int
	maxAge   time.Duration

	mu      sync.Mutex
	lru     *list.List // *cacheEntry, front = most recent
	entries map[string]*list.Element

	createMu sync.Mutex
	creates  map[string][]time.Time
}

type cacheEntry struct {
	id string
	ws *Workspace
}

// NewID returns a random 128-bit workspace id (hex).
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// OpenHosted creates a hosted-demo manager: workspaces under
// <dataDir>/workspaces built from <dataDir>/template.db. The template is
// rebuilt on every boot so it always matches the binary's migrations and
// seed data. secret signs cookies; when empty a random key is generated
// and persisted under dataDir so restarts keep existing sessions.
func OpenHosted(ctx context.Context, dataDir string, secret []byte, seed func(context.Context, *store.DB) error) (*Manager, error) {
	wsDir := filepath.Join(dataDir, "workspaces")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		return nil, err
	}
	if len(secret) == 0 {
		var err error
		secret, err = loadOrCreateSecret(filepath.Join(dataDir, "cookie.secret"))
		if err != nil {
			return nil, err
		}
	}
	m := &Manager{
		dir: wsDir, template: filepath.Join(dataDir, "template.db"),
		secret: secret, seed: seed,
		maxOpen: defaultMaxOpen, maxCount: defaultMaxCount, maxAge: defaultMaxAge,
		lru: list.New(), entries: map[string]*list.Element{},
		creates: map[string][]time.Time{},
	}
	if err := m.rebuildTemplate(ctx); err != nil {
		return nil, err
	}
	go m.cleanupLoop()
	return m, nil
}

// OpenLocal wraps a single database as a fixed workspace for local runs
// (no cookies, one shared demo).
func OpenLocal(db *store.DB, seed func(context.Context, *store.DB) error) *Manager {
	return &Manager{
		local: &Workspace{ID: "local", Label: "local demo", DB: db},
		seed:  seed,
	}
}

func loadOrCreateSecret(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if s, err := hex.DecodeString(strings.TrimSpace(string(data))); err == nil && len(s) == 32 {
			return s, nil
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(b)), 0o600); err != nil {
		return nil, err
	}
	return b, nil
}

// rebuildTemplate recreates the seeded template database at boot.
func (m *Manager) rebuildTemplate(ctx context.Context) error {
	for _, suf := range []string{"", "-wal", "-shm"} {
		os.Remove(m.template + suf)
	}
	db, err := store.Open(m.template)
	if err != nil {
		return fmt.Errorf("template db: %w", err)
	}
	if m.seed != nil {
		if err := m.seed(ctx, db); err != nil {
			db.Close()
			return fmt.Errorf("seeding template: %w", err)
		}
	}
	if err := db.Close(); err != nil {
		return err
	}
	return nil
}

// Hosted reports whether the manager serves per-visitor workspaces.
func (m *Manager) Hosted() bool { return m.local == nil }

// Local returns the fixed workspace in local mode.
func (m *Manager) Local() *Workspace { return m.local }

// Healthy reports whether the hosted demo storage looks usable
// (template present and workspaces dir writable).
func (m *Manager) Healthy() bool {
	if m.local != nil {
		return m.local.DB.Ping() == nil
	}
	if _, err := os.Stat(m.template); err != nil {
		return false
	}
	probe, err := os.CreateTemp(m.dir, ".probe-*")
	if err != nil {
		return false
	}
	probe.Close()
	os.Remove(probe.Name())
	return true
}

// sign returns the cookie signature for a workspace id.
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

func (m *Manager) sign(id string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(id))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// cookieID extracts and verifies a workspace id from the request cookie.
func (m *Manager) cookieID(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	id, sig, ok := strings.Cut(c.Value, ".")
	if !ok || len(id) != 32 {
		return ""
	}
	if subtle.ConstantTimeCompare([]byte(sig), []byte(m.sign(id))) != 1 {
		return ""
	}
	return id
}

// SetCookie writes the signed workspace cookie.
func (m *Manager) SetCookie(w http.ResponseWriter, r *http.Request, id string) {
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: id + "." + m.sign(id),
		Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: cookieMaxAgeDays * 24 * 3600,
	})
}

// clientIP extracts the client address for rate limiting.
func clientIP(r *http.Request) string {
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

// allowCreate rate-limits workspace creation per IP (sliding window).
func (m *Manager) allowCreate(ip string) bool {
	m.createMu.Lock()
	defer m.createMu.Unlock()
	cutoff := time.Now().Add(-time.Hour)
	keep := m.creates[ip][:0]
	for _, t := range m.creates[ip] {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	m.creates[ip] = keep
	if len(keep) >= createsPerHour {
		return false
	}
	m.creates[ip] = append(m.creates[ip], time.Now())
	return true
}

// Resolve returns the workspace for a request: the cookie's workspace in
// hosted mode (creating one and setting the cookie when absent), or the
// fixed local workspace.
func (m *Manager) Resolve(w http.ResponseWriter, r *http.Request) (*Workspace, error) {
	if m.local != nil {
		return m.local, nil
	}
	if id := m.cookieID(r); id != "" {
		if ws, err := m.Get(r.Context(), id); err == nil {
			return ws, nil
		}
		// Unknown/deleted workspace: fall through and create a fresh one.
	}
	if !m.allowCreate(clientIP(r)) {
		return nil, fmt.Errorf("too many new demo sessions from this address — try again later")
	}
	ws, err := m.Create(r.Context())
	if err != nil {
		return nil, err
	}
	m.SetCookie(w, r, ws.ID)
	return ws, nil
}

// New abandons the visitor's current workspace and starts a fresh one.
func (m *Manager) New(w http.ResponseWriter, r *http.Request) (*Workspace, error) {
	if m.local != nil {
		return m.local, nil
	}
	if !m.allowCreate(clientIP(r)) {
		return nil, fmt.Errorf("too many new demo sessions from this address — try again later")
	}
	ws, err := m.Create(r.Context())
	if err != nil {
		return nil, err
	}
	m.SetCookie(w, r, ws.ID)
	return ws, nil
}

// Get opens (or returns the cached) workspace by id.
func (m *Manager) Get(ctx context.Context, id string) (*Workspace, error) {
	if m.local != nil {
		if id == m.local.ID {
			return m.local, nil
		}
		return nil, fmt.Errorf("unknown workspace %q", id)
	}
	m.mu.Lock()
	if el, ok := m.entries[id]; ok {
		m.lru.MoveToFront(el)
		ws := el.Value.(*cacheEntry).ws
		m.mu.Unlock()
		m.touch(id)
		return ws, nil
	}
	m.mu.Unlock()

	path := filepath.Join(m.dir, id+".db")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("workspace %s not found", id)
	}
	db, err := store.Open(path) // applies any pending migrations
	if err != nil {
		return nil, err
	}
	// Refresh publication config from the current template so sessions
	// created before a deploy pick up fixture/config changes (price,
	// sections…). Visitor data (issues, decisions, sources, readers) is
	// preserved — SeedPublication only updates config columns.
	if m.seed != nil {
		if err := m.seed(ctx, db); err != nil {
			slog.Warn("workspace reseed failed", "ws", id, "err", err)
		}
	}
	ws := &Workspace{ID: id, Label: labelOf(id), Dir: m.dir, DB: db}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[id] = m.lru.PushFront(&cacheEntry{id: id, ws: ws})
	for m.lru.Len() > m.maxOpen {
		back := m.lru.Back()
		if back == nil {
			break
		}
		e := back.Value.(*cacheEntry)
		e.ws.DB.Close()
		delete(m.entries, e.id)
		m.lru.Remove(back)
	}
	m.touch(id)
	return ws, nil
}

// Create copies the template database into a new workspace.
func (m *Manager) Create(ctx context.Context) (*Workspace, error) {
	id := NewID()
	dst := filepath.Join(m.dir, id+".db")
	if err := copyFile(m.template, dst); err != nil {
		return nil, err
	}
	return m.Get(ctx, id)
}

// Reset wipes and reseeds one workspace's data, waiting for in-flight
// pipeline runs to finish.
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
	if m.seed != nil {
		return m.seed(ctx, ws.DB)
	}
	return nil
}

// touch refreshes the workspace file mtime (throttled) so the cleanup
// loop sees the workspace as in use.
func (m *Manager) touch(id string) {
	path := filepath.Join(m.dir, id+".db")
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) > touchInterval {
		now := time.Now()
		os.Chtimes(path, now, now)
	}
}

// CloseWorkspace drops a workspace's cached handle without deleting data.
func (m *Manager) CloseWorkspace(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if el, ok := m.entries[id]; ok {
		el.Value.(*cacheEntry).ws.DB.Close()
		delete(m.entries, id)
		m.lru.Remove(el)
	}
}

// Close releases every open workspace handle (local mode: the single one).
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.local != nil {
		m.local.DB.Close()
		return
	}
	for id, el := range m.entries {
		el.Value.(*cacheEntry).ws.DB.Close()
		delete(m.entries, id)
		m.lru.Remove(el)
	}
}

// cleanupLoop deletes abandoned workspaces and enforces the count cap.
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		m.cleanup()
	}
}

func (m *Manager) cleanup() {
	ents, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	type cand struct {
		path  string
		mtime time.Time
	}
	var files []cand
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, cand{filepath.Join(m.dir, name), info.ModTime()})
	}
	inUse := func(path string) bool {
		id := strings.TrimSuffix(filepath.Base(path), ".db")
		m.mu.Lock()
		defer m.mu.Unlock()
		_, ok := m.entries[id]
		return ok
	}
	remove := func(path string) {
		os.Remove(path)
		os.Remove(path + "-wal")
		os.Remove(path + "-shm")
	}
	// age out abandoned workspaces
	var live []cand
	for _, f := range files {
		if time.Since(f.mtime) > m.maxAge && !inUse(f.path) {
			remove(f.path)
			continue
		}
		live = append(live, f)
	}
	// enforce the count cap, evicting least recently used first
	for len(live) > m.maxCount {
		oldest := -1
		for i, f := range live {
			if inUse(f.path) {
				continue
			}
			if oldest < 0 || f.mtime.Before(live[oldest].mtime) {
				oldest = i
			}
		}
		if oldest < 0 {
			break
		}
		remove(live[oldest].path)
		live = append(live[:oldest], live[oldest+1:]...)
	}
}

func labelOf(id string) string {
	if len(id) > 4 {
		return "demo session " + id[:4] + "…"
	}
	return "demo session " + id
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
