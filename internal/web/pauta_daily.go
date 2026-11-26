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

// visiblePautaCards drops stored cards the creator already answered
// "Não" — the snapshot is served between builds, so the rejection
// filter the live build applies must also run on read: a snapshot
// written before the answer landed must not resurrect the story
// (KLOX-236). Snapshots predating card keys resolve them through the
// live story row; an unresolvable card stays — hiding needs a matched
// key, not a guess.
func visiblePautaCards(ctx context.Context, wdb *store.DB, pubID int64, views []pautaCardView) []pautaCardView {
	answers, err := wdb.RejectedStoryFeedback(ctx, pubID)
	if err != nil || len(answers) == 0 {
		return views
	}
	rejected := make(map[string]bool, len(answers))
	for _, a := range answers {
		rejected[a.Key] = true
	}
	out := make([]pautaCardView, 0, len(views))
	for _, v := range views {
		keys := v.Keys
		if len(keys) == 0 {
			if st, err := wdb.StoryWithItems(ctx, v.StoryID); err == nil && st != nil {
				keys = st.FeedbackKeys()
			}
		}
		drop := false
		for _, k := range keys {
			if rejected[k] {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, v)
		}
	}
	return out
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
	mailDue := s.d.DailyPautaMail && local.Hour() >= pautaMailHour
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
		PaceStep:      s.stepPacer(),
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

// mintPautaMailLink stores one hashed 72-hour magic link and returns its
// absolute /claim URL. next is an internal SPA path the confirmed session
// should land on; /claim validates it before redirecting.
func mintPautaMailLink(ctx context.Context, wdb *store.DB, owner, lang, base, next string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := wdb.SaveMagicLink(ctx, hashToken(token), owner,
		store.TSUTC(time.Now().Add(pautaMailLinkTTL))); err != nil {
		return "", err
	}
	link := base + "/claim?t=" + url.QueryEscape(token) + "&lang=" + lang
	if next != "" {
		link += "&next=" + url.QueryEscape(next)
	}
	return link, nil
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
	var cards []pautaCardView
	if err := json.Unmarshal([]byte(row.CardsJSON), &cards); err != nil {
		slog.Warn("pauta day: malformed cards json", "day", day, "err", err)
	}
	cards = visiblePautaCards(ctx, wdb, pub.ID, cards)
	// Sem notícias novas no dia, nada sai (KLOX-256/258): the mail
	// announces what the overnight build found — a pauta that is empty
	// or only repeats the previous snapshot has nothing to announce,
	// and skipping here keeps the owner's one-mail-a-day slot free for
	// another radar of theirs that does carry new stories.
	if !hasNewPautaCards(ctx, wdb, pub.ID, day, cards) {
		return wdb.MarkPautaDayEmailed(ctx, pub.ID, day)
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
	// The account opted out — the mailed "Não quero mais" link or the
	// account-menu toggle (KLOX-257): stamp the day and move on.
	if out, err := s.dailyMailOptedOut(ctx, pool, wdb, owner); err != nil {
		return err
	} else if out {
		return wdb.MarkPautaDayEmailed(ctx, pub.ID, day)
	}
	// One mail per recipient per day: an owner with several claimed
	// radars (or workspaces) gets a single note, not one per radar.
	if ok, err := pool.ClaimPautaMail(ctx, owner, day); err != nil {
		return err
	} else if !ok {
		return wdb.MarkPautaDayEmailed(ctx, pub.ID, day)
	}

	base := strings.TrimRight(s.d.Cfg.PublicURL, "/")
	if base == "" {
		base = fmt.Sprintf("http://localhost:%d", s.d.Cfg.Port)
	}
	// the sign-in link keeps the account's language — without it an EN
	// owner dropped back to PT after every mail click (KLOX-232)
	lang := wdb.UILang(ctx)
	if lang != "en" && lang != "pt" {
		lang = "pt"
	}

	// A fresh sign-in link so the mail opens the radar on any device —
	// same magic-link claim as "save your Kloxus", with a mail-sized TTL.
	// The confirmed session lands on today's briefing, not /start.
	openNext := fmt.Sprintf("/publications/%d/today", pub.ID)
	link, err := mintPautaMailLink(ctx, wdb, owner, lang, base, openNext)
	if err != nil {
		return err
	}

	// One real deep link per story (KLOX-267): the CTA signs the browser
	// in and drops the author in the composer with this story pre-linked.
	top := cards
	if len(top) > pautaMailTop {
		top = top[:pautaMailTop]
	}
	stories := make([]pautaMailStory, 0, len(top))
	for _, c := range top {
		next := fmt.Sprintf("/compor?pub=%d&noticia=%d", pub.ID, c.StoryID)
		postLink, err := mintPautaMailLink(ctx, wdb, owner, lang, base, next)
		if err != nil {
			return err
		}
		stories = append(stories, pautaMailStory{Card: c, PostURL: postLink})
	}

	// The mail itself speaks the account's language (KLOX-258): PT is
	// the default; an EN account gets the subject, greeting, button
	// and link notice in English.
	subject := "Suas notícias de hoje estão prontas"
	greeting := "Bom dia! As notícias do seu radar **%s** de hoje estão prontas:"
	button := "Abrir minhas notícias"
	notice := "O link assina este navegador na sua conta salva e vale por 72 horas."
	if lang == "en" {
		subject = "Your stories for today are ready"
		greeting = "Good morning! Today's stories for your Radar **%s**:"
		button = "Open my stories"
		notice = "The link signs this browser into your saved account and is valid for 72 hours."
	}

	// The one-click unsubscribe link (KLOX-257): its own token, stored
	// hashed — never the sign-in token, never a reader MAC. Each send
	// replaces the hash, so the newest mailed link is the one that works.
	rawUnsub := make([]byte, 32)
	if _, err := rand.Read(rawUnsub); err != nil {
		return err
	}
	unsubToken := base64.RawURLEncoding.EncodeToString(rawUnsub)
	if err := wdb.SaveDailyMailToken(ctx, hashToken(unsubToken)); err != nil {
		return err
	}
	unsubLink := base + "/daily-mail/unsub?t=" + url.QueryEscape(unsubToken)
	unsubLabel := "Não quero mais este e-mail"
	if lang == "en" {
		unsubLabel = "I don't want this email anymore"
	}

	var b strings.Builder
	fmt.Fprintf(&b, greeting+"\n\n", pub.Name)
	for i, st := range stories {
		c := st.Card
		fmt.Fprintf(&b, "%d. %s", i+1, c.Title)
		if c.Outlet != "" {
			b.WriteString(" — " + c.Outlet)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "[%s](%s)\n\n%s", button, link, notice)
	if len(stories) > 0 {
		b.WriteString("\n\n")
		if lang == "en" {
			b.WriteString("Create a post from a story:")
		} else {
			b.WriteString("Criar post a partir de uma notícia:")
		}
		for i, st := range stories {
			fmt.Fprintf(&b, "\n%d. %s", i+1, st.PostURL)
		}
	}
	fmt.Fprintf(&b, "\n\n---\n*[%s](%s)*", unsubLabel, unsubLink)

	md := b.String()
	if _, err := s.sendMail(ctx, wdb, &delivery.Message{
		To:      []string{owner},
		Subject: subject,
		Text:    md,
		HTML:    renderPautaMailHTML(lang, day, pub.Name, stories, link, unsubLink),
		Kind:    "pauta",
		// RFC 8058: the same endpoint the footer link lands on — mail
		// clients POST it for one-click unsubscribes (KLOX-257).
		Headers: map[string]string{
			"List-Unsubscribe":      "<" + unsubLink + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
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

// hasNewPautaCards reports whether today's pauta offers a story the
// radar did not already offer — the "notícias novas no dia" the 07:00
// mail exists for (KLOX-256). A card counts as new when none of its
// stable feedback keys (canonical URL, SimHash) appeared in the
// previous stored pauta; a card without keys cannot be proven old and
// counts as new, as does every card of a first-ever snapshot.
func hasNewPautaCards(ctx context.Context, wdb *store.DB, pubID int64, day string, cards []pautaCardView) bool {
	if len(cards) == 0 {
		return false
	}
	prev, err := wdb.PrevPautaDay(ctx, pubID, day)
	if err != nil {
		return true // no earlier snapshot — everything is new
	}
	var prevCards []pautaCardView
	if err := json.Unmarshal([]byte(prev.CardsJSON), &prevCards); err != nil {
		return true // an unreadable snapshot can't prove a repeat
	}
	seen := make(map[string]bool)
	for _, c := range prevCards {
		for _, k := range c.Keys {
			seen[k] = true
		}
	}
	for _, c := range cards {
		keys := c.Keys
		if len(keys) == 0 {
			// same resolution as the rejection filter above: snapshots
			// predating card keys look the story up live
			if st, err := wdb.StoryWithItems(ctx, c.StoryID); err == nil && st != nil {
				keys = st.FeedbackKeys()
			}
		}
		fresh := len(keys) == 0
		for _, k := range keys {
			if !seen[k] {
				fresh = true
				break
			}
		}
		if fresh {
			return true
		}
	}
	return false
}
