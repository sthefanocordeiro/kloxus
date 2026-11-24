package web

// The daily pauta sweep (KLOX-123, pivot Block 2): at 06:30
// America/Sao_Paulo the global scheduler rebuilds the "pauta de hoje"
// for every radar visited in the last 7 days — stage-1 + MMR over what
// the shared ingest collected, card summaries from the cache, zero
// model calls — and stores it so Hoje opens ready. At 07:00 a "sua
// pauta de hoje está pronta" mail goes to radars with a saved account
// (one transactional mail per day, per workspace).

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata" // America/Sao_Paulo on hosts and images without zoneinfo

	"github.com/sthefanocordeiro/kloxus/internal/delivery"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

const (
	pautaBuildHour, pautaBuildMin = 6, 30
	pautaMailHour                 = 7
	pautaActiveWindow             = 7 * 24 * time.Hour
	pautaMailLinkTTL              = 72 * time.Hour
	pautaMailTop                  = 3
	// pautaRadarBudget bounds one radar's build+mail inside the sweep:
	// a live BuildStories takes seconds, so a stuck radar that eats the
	// whole tick is pathological — cut it and let the next tick retry.
	pautaRadarBudget = 45 * time.Second
)

// pautaLoc is the schedule's timezone. Brazil dropped DST in 2019, so
// America/Sao_Paulo is a fixed UTC-3 — the FixedZone fallback is exact.
var pautaLoc = func() *time.Location {
	if l, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return l
	}
	return time.FixedZone("America/Sao_Paulo", -3*3600)
}()

// pautaDayOf is the stored pauta's day key: the São Paulo date.
func pautaDayOf(t time.Time) string { return t.In(pautaLoc).Format("2006-01-02") }

// dropPautaDay removes today's stored pauta after an interaction that
// changes the ranking (Sim/Não, skip, Escrever, a new calibration
// deal) — the next Hoje rebuilds live.
func dropPautaDay(r *http.Request, wdb *store.DB, pubID int64) {
	_ = wdb.DeletePautaDay(r.Context(), pubID, pautaDayOf(time.Now()))
}

// RunDailyPauta ticks until ctx ends. Each tick builds the missing
// pauta for the current São Paulo day (after 06:30) and mails claimed
// radars (after 07:00). Both sides are idempotent at the row level, so
// a restart or a slow sweep simply resumes where pauta_days says it
// stopped — a build skipped at 06:30 still runs before the 07:00 mail.
func (s *Server) RunDailyPauta(ctx context.Context, pool *store.Pool, interval time.Duration) {
	if pool == nil {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			// Bound the sweep like a dispatch cycle: a dead pooled
			// conn must not wedge the only daily goroutine for good.
			tctx, cancel := context.WithTimeout(ctx, dispatchTimeout)
			s.DailyPautaTick(tctx, pool, now)
			cancel()
		}
	}
}

// DailyPautaTick runs one sweep at the given instant: one build pass
// then one mail pass over the workspaces visited in the last 7 days.
// Exported so tests drive it with a fixed clock.
func (s *Server) DailyPautaTick(ctx context.Context, pool *store.Pool, now time.Time) {
	local := now.In(pautaLoc)
	day := pautaDayOf(now)
	buildDue := local.Hour() > pautaBuildHour ||
		(local.Hour() == pautaBuildHour && local.Minute() >= pautaBuildMin)
	mailDue := local.Hour() >= pautaMailHour
	if !buildDue && !mailDue {
		return
	}
	wss, err := pool.ActiveWorkspaces(ctx, store.TSUTC(now.Add(-pautaActiveWindow)))
	if err != nil {
		slog.Warn("daily pauta: active workspaces failed", "err", err)
		return
	}
	start := time.Now()
	seen := 0
	for _, wsID := range wss {
		if ctx.Err() != nil {
			break // sweep budget spent — the next tick resumes the tail
		}
		seen++
		wdb := pool.For(wsID)
		pubs, err := wdb.ListPublications(ctx)
		if err != nil {
			slog.Warn("daily pauta: publications failed", "ws", wsID, "err", err)
			continue
		}
		for _, pub := range pubs {
			pctx, cancel := context.WithTimeout(ctx, pautaRadarBudget)
			if buildDue {
				if err := s.buildPautaDay(pctx, wdb, pub, day); err != nil {
					slog.Warn("daily pauta: build failed", "ws", wsID, "pub", pub.ID, "err", err)
				}
			}
			if mailDue {
				if err := s.mailPautaDay(pctx, pool, wdb, pub, wsID, day); err != nil {
					slog.Warn("daily pauta: mail failed", "ws", wsID, "pub", pub.ID, "err", err)
				}
			}
			cancel()
		}
	}
	// The summary line is the sweep's heartbeat in hosted logs: how far
	// it got before the tick budget ran out (seen < len(wss) means the
	// tail waits for the next tick — recency order keeps that harmless).
	slog.Info("daily pauta: sweep", "day", day, "workspaces", len(wss), "seen", seen, "took", time.Since(start).Round(time.Second))
}

// buildPautaDay stores the day's ranked pauta for one radar — a no-op
// when the row already exists. BuildStories refreshes the story set the
// ranking reads (catalog items arrive through the shared ingest, never
// fetched here); Pauta ranks stage-1 + MMR with cached summaries — the
// deterministic runner makes a model call impossible.
func (s *Server) buildPautaDay(ctx context.Context, wdb *store.DB, pub *store.Publication, day string) error {
	if _, err := wdb.PautaDay(ctx, pub.ID, day); err == nil {
		return nil // built already
	} else if err != sql.ErrNoRows {
		return err
	}
	run := &pipeline.Runner{
		DB: wdb, Fetcher: s.d.Fetcher,
		DemoMode:      s.d.Cfg.Mode == "demo",
		Deterministic: true,
	}
	stories, err := run.BuildStories(ctx, pub)
	if err != nil {
		return err
	}
	views := pautaViews(run.Pauta(ctx, pub, stories, todayCards, todayWindow))
	cards, err := json.Marshal(views)
	if err != nil {
		return err
	}
	if err := wdb.SavePautaDay(ctx, pub.ID, day, string(cards)); err != nil {
		return err
	}
	slog.Info("daily pauta: built", "ws", wdb.Workspace(), "pub", pub.ID, "day", day, "cards", len(views))
	return nil
}

// mailPautaDay sends the one daily "pauta pronta" mail to a radar with
// a saved account — skipped silently for anonymous workspaces and any
// radar whose day was already handled (sent, refused or skipped).
func (s *Server) mailPautaDay(ctx context.Context, pool *store.Pool, wdb *store.DB, pub *store.Publication, wsID, day string) error {
	row, err := wdb.PautaDay(ctx, pub.ID, day)
	if err == sql.ErrNoRows {
		return nil // nothing built (before 06:30 or the build failed)
	}
	if err != nil {
		return err
	}
	if row.EmailedAt != "" {
		return nil
	}
	owner, _, err := pool.WorkspaceOwner(ctx, wsID)
	if err != nil {
		return err
	}
	if owner == "" {
		// No saved account: nothing to mail — stamp it anyway so the
		// sweep never re-checks this radar today.
		return wdb.MarkPautaDayEmailed(ctx, pub.ID, day)
	}
	// One mail per recipient per day: an owner with several claimed
	// radars (or workspaces) gets a single note, not one per radar.
	if ok, err := pool.ClaimPautaMail(ctx, owner, day); err != nil {
		return err
	} else if !ok {
		return wdb.MarkPautaDayEmailed(ctx, pub.ID, day)
	}

	var cards []pautaCardView
	_ = json.Unmarshal([]byte(row.CardsJSON), &cards)
	base := strings.TrimRight(s.d.Cfg.PublicURL, "/")
	if base == "" {
		base = fmt.Sprintf("http://localhost:%d", s.d.Cfg.Port)
	}
	// A fresh sign-in link so the mail opens the radar on any device —
	// same magic-link claim as "save your Kloxus", with a mail-sized TTL.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := wdb.SaveMagicLink(ctx, hashToken(token), owner,
		store.TSUTC(time.Now().Add(pautaMailLinkTTL))); err != nil {
		return err
	}
	link := base + "/claim?t=" + url.QueryEscape(token) + "&lang=pt"

	var b strings.Builder
	fmt.Fprintf(&b, "Bom dia! As notícias do seu radar **%s** de hoje estão prontas:\n\n", pub.Name)
	for i, c := range cards {
		if i >= pautaMailTop {
			break
		}
		fmt.Fprintf(&b, "%d. %s", i+1, c.Title)
		if c.Outlet != "" {
			b.WriteString(" — " + c.Outlet)
		}
		b.WriteString("\n")
	}
	if len(cards) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "[Abrir minhas notícias](%s)\n\nO link assina este navegador na sua conta salva e vale por 72 horas.", link)

	subject := "Suas notícias de hoje estão prontas"
	md := b.String()
	if _, err := s.sendMail(ctx, wdb, &delivery.Message{
		To:      []string{owner},
		Subject: subject,
		Text:    md,
		HTML:    delivery.RenderHTML(subject, md, base),
		Kind:    "pauta",
		// Provider idempotency is global per API key, not per
		// workspace — scope the key or the second claimed radar of the
		// day collides on a modified body and falls back to the outbox.
		IdemKey: fmt.Sprintf("pauta/%s/%s", wsID, day),
	}); err != nil {
		return err
	}
	// Refused and fallback are the day's one attempt too — the ledger
	// row says why, and the spec asks for one mail a day.
	return wdb.MarkPautaDayEmailed(ctx, pub.ID, day)
}
