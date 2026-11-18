package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// TestRankedCandidatesBatched covers the single-query candidates path:
// ranking, the in-issue join, the dossier EXISTS flag and the duplicate
// count must all come back correct without per-candidate lookups.
// StoryOutlets must report every member outlet in item order.
func TestRankedCandidatesBatched(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, err := db.Sources(ctx, pubID)
	if err != nil || len(srcs) == 0 {
		t.Fatalf("Sources: %v len=%d", err, len(srcs))
	}
	srcA := srcs[0].ID // unnamed fixture source — outlet falls back to URL
	srcB, err := db.AddSource(ctx, pubID, "rss", "https://b.example/feed", "Outlet B")
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}

	addItem := func(sourceID int64, ext string) int64 {
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title)
			 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			db.ws, sourceID, ext, "https://x.example/"+ext, "item "+ext).Scan(&id); err != nil {
			t.Fatalf("insert item %s: %v", ext, err)
		}
		return id
	}
	addStory := func(i int, score float64, itemIDs ...int64) int64 {
		sid, err := db.CreateStory(ctx, pubID, fmt.Sprintf("story %02d", i), int64(i), "")
		if err != nil {
			t.Fatalf("CreateStory: %v", err)
		}
		for _, it := range itemIDs {
			if err := db.AddStoryItem(ctx, sid, it); err != nil {
				t.Fatalf("AddStoryItem: %v", err)
			}
		}
		if err := db.SetStoryTriage(ctx, sid, score, "reason"); err != nil {
			t.Fatalf("SetStoryTriage: %v", err)
		}
		return sid
	}

	issueID, err := db.CreateIssue(ctx, pubID, 1, "s", "", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	otherID, err := db.CreateIssue(ctx, pubID, 2, "s2", "", nil)
	if err != nil {
		t.Fatalf("CreateIssue 2: %v", err)
	}

	// 25 ranked stories, descending scores → rank must track score order.
	const N = 25
	storyIDs := make([]int64, N)
	for i := 0; i < N; i++ {
		items := []int64{addItem(srcA, fmt.Sprintf("a-%d", i))}
		if i == 0 {
			// top story covered by a second outlet too
			items = append(items, addItem(srcB, "b-0"))
		}
		storyIDs[i] = addStory(i, float64(N-i), items...)
	}
	// an untriaged story never appears, and a story already placed in
	// another issue is out of the pool
	extra, err := db.CreateStory(ctx, pubID, "untriaged", 999, "")
	if err != nil {
		t.Fatalf("CreateStory untriaged: %v", err)
	}
	_ = extra
	taken := addStory(N, 0.5, addItem(srcA, "a-taken"))
	if _, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: otherID, StoryID: taken, Section: "Lead", Status: "approved",
	}); err != nil {
		t.Fatalf("CreateIssueItem other issue: %v", err)
	}

	// two stories sit in this issue; one carries a dossier
	in1, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: issueID, StoryID: storyIDs[0], Section: "Lead",
		Score: 25, Reason: "r", Status: "approved", Position: 0,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem in1: %v", err)
	}
	in2, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: issueID, StoryID: storyIDs[2], Section: "Lead",
		Score: 23, Reason: "r", Status: "pending", Position: 1,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem in2: %v", err)
	}
	if err := db.SaveDossier(ctx, in1, "{}"); err != nil {
		t.Fatalf("SaveDossier: %v", err)
	}

	cands, err := db.RankedCandidates(ctx, pubID, issueID)
	if err != nil {
		t.Fatalf("RankedCandidates: %v", err)
	}
	if len(cands) != N {
		t.Fatalf("candidates = %d, want %d (taken story in other issue must be excluded)", len(cands), N)
	}
	prev := cands[0].Story.TriageScore
	for i, c := range cands {
		if c.Rank != i+1 {
			t.Fatalf("rank at %d = %d", i, c.Rank)
		}
		if c.Story.TriageScore > prev {
			t.Fatal("candidates not ordered by score desc")
		}
		prev = c.Story.TriageScore
		switch c.Story.ID {
		case storyIDs[0]:
			if c.Item == nil || c.Item.ID != in1 || !c.Dossier {
				t.Fatalf("top candidate = %+v, want issue item %d with dossier", c, in1)
			}
			if c.Item.Section != "Lead" || c.Item.Status != "approved" {
				t.Fatalf("joined item fields wrong: %+v", c.Item)
			}
			if c.Duplicates != 1 {
				t.Fatalf("duplicates = %d, want 1 (two member items)", c.Duplicates)
			}
		case storyIDs[2]:
			if c.Item == nil || c.Item.ID != in2 || c.Dossier {
				t.Fatalf("pending candidate = %+v, want item %d without dossier", c, in2)
			}
		case taken:
			t.Fatal("story placed in another issue leaked into candidates")
		default:
			if c.Item != nil {
				t.Fatalf("bench story %d unexpectedly joined item %+v", c.Story.ID, c.Item)
			}
		}
	}

	ids := make([]int64, N)
	for i, sid := range storyIDs {
		ids[i] = sid
	}
	outlets, err := db.StoryOutlets(ctx, ids)
	if err != nil {
		t.Fatalf("StoryOutlets: %v", err)
	}
	if len(outlets[storyIDs[0]]) != 2 {
		t.Fatalf("top story outlets = %v, want 2 members", outlets[storyIDs[0]])
	}
	if outlets[storyIDs[0]][1].Name != "Outlet B" {
		t.Fatalf("second outlet = %+v, want named Outlet B", outlets[storyIDs[0]][1])
	}
	if got := outlets[storyIDs[1]]; len(got) != 1 || got[0].URL != srcs[0].URL {
		t.Fatalf("single-member outlets = %v, want the unnamed source URL", got)
	}
	if empty, err := db.StoryOutlets(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("StoryOutlets(nil) = %v %v", empty, err)
	}
}

// TestCreateStoriesBatched exercises the run-scale dedupe write path:
// ~5k stored items grouped into ~2.5k clusters must persist in two
// statements (KLOX-41), with ids pairing positionally to drafts, and
// the batch triage update must land in one statement too.
func TestCreateStoriesBatched(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, err := db.Sources(ctx, pubID)
	if err != nil || len(srcs) == 0 {
		t.Fatalf("Sources: %v len=%d", err, len(srcs))
	}
	// 5000 items — the shared-catalog pool size that timed out on Fly.
	const N = 5000
	if _, err := db.ExecContext(ctx, `
		INSERT INTO items (workspace_id, source_id, external_id, url, title)
		SELECT $1, $2, 'it-' || g, 'https://x.example/' || g, 'item ' || g
		FROM generate_series(1, $3) g`, db.ws, srcs[0].ID, N); err != nil {
		t.Fatalf("insert items: %v", err)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM items WHERE workspace_id = $1 AND source_id = $2 ORDER BY id`, db.ws, srcs[0].ID)
	if err != nil {
		t.Fatalf("select item ids: %v", err)
	}
	var itemIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan item id: %v", err)
		}
		itemIDs = append(itemIDs, id)
	}
	rows.Close()
	if len(itemIDs) != N {
		t.Fatalf("items = %d, want %d", len(itemIDs), N)
	}

	// Two items per cluster, mirroring the QA pool shape; give cluster 0
	// a duplicate member id to exercise the ON CONFLICT path.
	var drafts []StoryDraft
	for i := 0; i+1 < len(itemIDs); i += 2 {
		members := []int64{itemIDs[i], itemIDs[i+1]}
		if len(drafts) == 0 {
			members = append(members, itemIDs[i])
		}
		drafts = append(drafts, StoryDraft{
			Title:     fmt.Sprintf("story %d", i/2),
			Simhash:   int64(i),
			DedupeKey: fmt.Sprintf("key-%d", i/2),
			ItemIDs:   members,
		})
	}
	if err := db.CreateStories(ctx, pubID, drafts); err != nil {
		t.Fatalf("CreateStories: %v", err)
	}
	if err := db.CreateStories(ctx, pubID, nil); err != nil {
		t.Fatalf("CreateStories(nil): %v", err)
	}

	var storyCount, linkCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM stories WHERE workspace_id = $1 AND publication_id = $2`,
		db.ws, pubID).Scan(&storyCount); err != nil {
		t.Fatalf("count stories: %v", err)
	}
	if storyCount != len(drafts) {
		t.Fatalf("stories = %d, want %d", storyCount, len(drafts))
	}
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM story_items WHERE workspace_id = $1`, db.ws).Scan(&linkCount); err != nil {
		t.Fatalf("count story_items: %v", err)
	}
	if linkCount != N {
		t.Fatalf("story_items = %d, want %d (dup member must collapse)", linkCount, N)
	}
	// Positional pairing: story 0's title/dedupe_key must sit on the row
	// that owns the first items — a shuffled RETURNING would mislink.
	var title, key string
	if err := db.QueryRowContext(ctx, `
		SELECT s.title, s.dedupe_key FROM stories s
		WHERE s.workspace_id = $1 AND s.id = (
			SELECT story_id FROM story_items WHERE workspace_id = $1 AND item_id = $2)`,
		db.ws, itemIDs[0]).Scan(&title, &key); err != nil {
		t.Fatalf("story of first item: %v", err)
	}
	if title != "story 0" || key != "key-0" {
		t.Fatalf("first story = %q/%q, want story 0/key-0", title, key)
	}

	rows, err = db.QueryContext(ctx,
		`SELECT id FROM stories WHERE workspace_id = $1 AND publication_id = $2 ORDER BY id`, db.ws, pubID)
	if err != nil {
		t.Fatalf("select story ids: %v", err)
	}
	var storyIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan story id: %v", err)
		}
		storyIDs = append(storyIDs, id)
	}
	rows.Close()
	updates := make([]StoryTriage, len(storyIDs))
	for i, sid := range storyIDs {
		updates[i] = StoryTriage{StoryID: sid, Score: float64(i), Reason: fmt.Sprintf("r%d", i)}
	}
	if err := db.SetStoriesTriage(ctx, updates); err != nil {
		t.Fatalf("SetStoriesTriage: %v", err)
	}
	var scored int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM stories WHERE workspace_id = $1 AND publication_id = $2 AND triage_score IS NOT NULL`,
		db.ws, pubID).Scan(&scored); err != nil {
		t.Fatalf("count scored: %v", err)
	}
	if scored != len(drafts) {
		t.Fatalf("scored = %d, want %d", scored, len(drafts))
	}
	var gotScore float64
	var gotReason string
	if err := db.QueryRowContext(ctx,
		`SELECT triage_score, triage_reason FROM stories WHERE workspace_id = $1 AND title = 'story 7'`,
		db.ws).Scan(&gotScore, &gotReason); err != nil {
		t.Fatalf("verdict of story 7: %v", err)
	}
	if gotScore != 7 || gotReason != "r7" {
		t.Fatalf("story 7 verdict = %v/%q, want 7/r7", gotScore, gotReason)
	}
	if err := db.SetStoriesTriage(ctx, nil); err != nil {
		t.Fatalf("SetStoriesTriage(nil): %v", err)
	}
}

// TestIssueItemDossiersAndGetItems covers the document builder's two
// batch queries (KLOX-47): the issue-items+dossier join must return
// every lineup item with its dossier JSON in score order, and GetItems
// must resolve an id list in one query under the same
// workspace-or-catalog visibility rule as GetItem.
func TestIssueItemDossiersAndGetItems(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, err := db.Sources(ctx, pubID)
	if err != nil || len(srcs) == 0 {
		t.Fatalf("Sources: %v len=%d", err, len(srcs))
	}
	issueID, err := db.CreateIssue(ctx, pubID, 1, "s", "", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	story, err := db.CreateStory(ctx, pubID, "story", 1, "")
	if err != nil {
		t.Fatalf("CreateStory: %v", err)
	}
	lo, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: issueID, StoryID: story, Section: "Lead",
		Score: 10, Status: "pending", Position: 0,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem lo: %v", err)
	}
	hi, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: issueID, StoryID: story, Section: "Lead",
		Score: 20, Status: "approved", Position: 1,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem hi: %v", err)
	}
	if err := db.SaveDossier(ctx, lo, `{"ref":3}`); err != nil {
		t.Fatalf("SaveDossier: %v", err)
	}

	pairs, err := db.IssueItemDossiers(ctx, issueID)
	if err != nil {
		t.Fatalf("IssueItemDossiers: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("pairs = %d, want 2", len(pairs))
	}
	// score desc, like IssueItems
	if pairs[0].Item.ID != hi || pairs[1].Item.ID != lo {
		t.Fatalf("pair order = %d,%d, want %d,%d", pairs[0].Item.ID, pairs[1].Item.ID, hi, lo)
	}
	if pairs[0].Dossier != "" {
		t.Fatalf("dossierless item returned %q", pairs[0].Dossier)
	}
	if pairs[1].Dossier != `{"ref":3}` {
		t.Fatalf("dossier json = %q", pairs[1].Dossier)
	}
	if pairs[1].Item.Section != "Lead" || pairs[1].Item.Status != "pending" {
		t.Fatalf("joined item fields wrong: %+v", pairs[1].Item)
	}

	var i1, i2 int64
	for i, ext := range []string{"g1", "g2"} {
		var id int64
		if err := db.QueryRowContext(ctx,
			`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text)
			 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			db.ws, srcs[0].ID, ext, "https://x.example/"+ext, "item "+ext,
			fmt.Sprintf("body %d", i)).Scan(&id); err != nil {
			t.Fatalf("insert item: %v", err)
		}
		if i == 0 {
			i1 = id
		} else {
			i2 = id
		}
	}
	got, err := db.GetItems(ctx, []int64{i1, i2, 99999999})
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetItems returned %d rows, want 2 (missing id skipped)", len(got))
	}
	if got[i1].Title != "item g1" || got[i1].ContentText != "body 0" || got[i1].SourceID != srcs[0].ID {
		t.Fatalf("GetItems row wrong: %+v", got[i1])
	}
	if got[i2].URL != "https://x.example/g2" {
		t.Fatalf("GetItems row wrong: %+v", got[i2])
	}
	// single row equals GetItem's shape
	one, err := db.GetItem(ctx, i2)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got[i2].ID != one.ID || got[i2].Title != one.Title || got[i2].URL != one.URL ||
		got[i2].ContentText != one.ContentText || got[i2].FetchedAt != one.FetchedAt {
		t.Fatalf("GetItems row differs from GetItem: %+v vs %+v", got[i2], one)
	}
	if empty, err := db.GetItems(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("GetItems(nil) = %v %v", empty, err)
	}
}

// TestIssueDocBundle covers the single-round-trip document context:
// issue + publication + item/dossier pairs + cited items must equal
// the multi-query path's results (KLOX-47 hosted latency criteria).
func TestIssueDocBundle(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, err := db.Sources(ctx, pubID)
	if err != nil || len(srcs) == 0 {
		t.Fatalf("Sources: %v len=%d", err, len(srcs))
	}
	// testPub seeds sources nameless; a real name exercises the
	// COALESCE(s.name, c.name) path instead of the '' fallback
	if _, err := db.ExecContext(ctx,
		`UPDATE sources SET name = 'Feed A' WHERE workspace_id = $1 AND id = $2`,
		db.ws, srcs[0].ID); err != nil {
		t.Fatalf("name source: %v", err)
	}
	issueID, err := db.CreateIssue(ctx, pubID, 1, "s", "Body [1].", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	story, err := db.CreateStory(ctx, pubID, "story", 1, "")
	if err != nil {
		t.Fatalf("CreateStory: %v", err)
	}
	var cited int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, author)
		 VALUES ($1,$2,'ext1','https://x.example/a','t1','body1','me') RETURNING id`,
		db.ws, srcs[0].ID).Scan(&cited); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	var other int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text)
		 VALUES ($1,$2,'ext2','https://x.example/b','t2','body2') RETURNING id`,
		db.ws, srcs[0].ID).Scan(&other); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	lo, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: issueID, StoryID: story, Section: "Lead",
		Score: 10, Status: "pending", Position: 0,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem lo: %v", err)
	}
	hi, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: issueID, StoryID: story, Section: "Lead",
		Score: 20, Status: "approved", Position: 1,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem hi: %v", err)
	}
	if err := db.SaveDossier(ctx, lo,
		fmt.Sprintf(`{"ref":3,"sources":[{"ref":1,"item_id":%d}]}`, cited)); err != nil {
		t.Fatalf("SaveDossier: %v", err)
	}
	if err := db.ResolveFlags(ctx, issueID, []Flag{{Item: 3, Sentence: "s", Note: "n"}}); err != nil {
		t.Fatalf("ResolveFlags: %v", err)
	}

	b, err := db.IssueDocBundle(ctx, issueID)
	if err != nil {
		t.Fatalf("IssueDocBundle: %v", err)
	}
	if b.Issue.ID != issueID || b.Issue.BodyMD != "Body [1]." || len(b.Issue.Flags) != 1 {
		t.Fatalf("issue fields wrong: %+v", b.Issue)
	}
	if b.Pub.ID != pubID || b.Pub.Slug == "" || len(b.Pub.Sections) == 0 {
		t.Fatalf("publication fields wrong: %+v", b.Pub)
	}
	if len(b.Pairs) != 2 || b.Pairs[0].Item.ID != hi || b.Pairs[1].Item.ID != lo {
		t.Fatalf("pairs wrong: %+v", b.Pairs)
	}
	if b.Pairs[0].Dossier != "" || b.Pairs[1].Dossier == "" {
		t.Fatalf("dossier join wrong: %+v", b.Pairs)
	}
	if len(b.Items) != 1 || b.Items[cited] == nil {
		t.Fatalf("cited items = %d, want the one cited id", len(b.Items))
	}
	if b.Items[other] != nil {
		t.Fatalf("uncited item leaked into bundle")
	}
	got := b.Items[cited]
	if got.SourceName != "Feed A" || got.SourceType != "rss" {
		t.Fatalf("joined source fields wrong: %+v", got)
	}
	// the contract is parity with the per-item path the bundle replaced
	one, err := db.GetItem(ctx, cited)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if !reflect.DeepEqual(*got, *one) {
		t.Fatalf("bundle item differs from GetItem:\nbundle:  %+v\ngetitem: %+v", got, one)
	}

	// issue without dossiers still resolves with an empty pair/item set
	empty, err := db.CreateIssue(ctx, pubID, 2, "s2", "", nil)
	if err != nil {
		t.Fatalf("CreateIssue 2: %v", err)
	}
	b2, err := db.IssueDocBundle(ctx, empty)
	if err != nil {
		t.Fatalf("IssueDocBundle empty: %v", err)
	}
	if len(b2.Pairs) != 0 || len(b2.Items) != 0 {
		t.Fatalf("empty issue bundle = %+v", b2)
	}
	if _, err := db.IssueDocBundle(ctx, 99999999); err == nil {
		t.Fatal("missing issue must error")
	}
}

// TestSaveIssueEdit covers the single-statement mutation: body + flags +
// decision row + the approved→drafted revert (receipt delete, queued
// send cancel) all land atomically in one CTE.
func TestSaveIssueEdit(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	pubID, err := db.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	issueID, err := db.CreateIssue(ctx, pubID, 1, "s", "old body", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	story, err := db.CreateStory(ctx, pubID, "story", 1, "")
	if err != nil {
		t.Fatalf("CreateStory: %v", err)
	}
	ii, err := db.CreateIssueItem(ctx, IssueItem{
		IssueID: issueID, StoryID: story, Section: "Lead",
		Score: 10, Status: "pending", Position: 0,
	})
	if err != nil {
		t.Fatalf("CreateIssueItem: %v", err)
	}

	// body + flags + decision, no revert
	err = db.SaveIssueEdit(ctx, issueID, IssueEdit{
		PubID: pubID, IssueItemID: ii, Body: "new body",
		Flags:    []Flag{{Item: 1, Sentence: "s", Note: "n"}},
		Decision: "sentence_edit", Reason: "r",
	})
	if err != nil {
		t.Fatalf("SaveIssueEdit: %v", err)
	}
	iss, err := db.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.BodyMD != "new body" || len(iss.Flags) != 1 || iss.Flags[0].Note != "n" {
		t.Fatalf("issue after edit: %+v", iss)
	}
	var nd int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM decisions WHERE workspace_id = $1 AND kind = 'sentence_edit' AND issue_item_id = $2`,
		db.ws, ii).Scan(&nd); err != nil || nd != 1 {
		t.Fatalf("decision row: n=%d err=%v", nd, err)
	}

	// nil flags leave flags_json alone; empty decision inserts nothing
	err = db.SaveIssueEdit(ctx, issueID, IssueEdit{PubID: pubID, Body: "again"})
	if err != nil {
		t.Fatalf("SaveIssueEdit 2: %v", err)
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.BodyMD != "again" || len(iss.Flags) != 1 {
		t.Fatalf("nil flags must keep flags_json: %+v", iss)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM decisions WHERE workspace_id = $1`,
		db.ws).Scan(&nd); err != nil || nd != 1 {
		t.Fatalf("empty decision must not insert: n=%d err=%v", nd, err)
	}

	// approved issue: revert folds status + receipt + queued send into
	// the same statement
	if err := db.SetIssueStatus(ctx, issueID, "approved"); err != nil {
		t.Fatalf("SetIssueStatus: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO approval_receipts (workspace_id, issue_id, number, document_version, approver)
		 VALUES ($1,$2,1,'v','tester')`, db.ws, issueID); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO jobs (workspace_id, kind, ref_id, run_at) VALUES ($1,'send_issue',$2,'x')`,
		db.ws, issueID); err != nil {
		t.Fatalf("job: %v", err)
	}
	err = db.SaveIssueEdit(ctx, issueID, IssueEdit{
		PubID: pubID, Body: "post-approval edit", RevertApproved: true,
	})
	if err != nil {
		t.Fatalf("SaveIssueEdit revert: %v", err)
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.Status != "drafted" || iss.BodyMD != "post-approval edit" {
		t.Fatalf("revert failed: %+v", iss)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM approval_receipts WHERE issue_id = $1`, issueID).Scan(&nd); err != nil || nd != 0 {
		t.Fatalf("receipt must be deleted: n=%d err=%v", nd, err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE ref_id = $1 AND status = 'pending'`, issueID).Scan(&nd); err != nil || nd != 0 {
		t.Fatalf("queued send must be cancelled: n=%d err=%v", nd, err)
	}

	// RevertApproved on a non-approved issue is a no-op for status
	err = db.SaveIssueEdit(ctx, issueID, IssueEdit{
		PubID: pubID, Body: "still drafted", RevertApproved: true,
	})
	if err != nil {
		t.Fatalf("SaveIssueEdit noop revert: %v", err)
	}
	iss, _ = db.GetIssue(ctx, issueID)
	if iss.Status != "drafted" {
		t.Fatalf("status = %q, want drafted", iss.Status)
	}
}
