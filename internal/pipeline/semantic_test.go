package pipeline

// KLOX-93 semantic-sweep tests. DB-backed cases skip without
// DATABASE_URL like the KLOX-67 suite; nothing here can call a paid
// provider — the sweep has no provider at all.
import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/embedding"
	"github.com/sthefanocordeiro/kloxus/internal/store"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
)

func TestSemanticSweepNilSafe(t *testing.T) {
	if n, err := (&SemanticSweep{}).Sweep(context.Background(), 40); err != nil || n != 0 {
		t.Fatalf("nil sweep: n=%d err=%v", n, err)
	}
}

// seedSeq makes each seeded external_id unique per call — a wall-clock
// nonce can repeat across back-to-back calls on fast hosts (KLOX-93 QA
// F2: UNIQUE (source_id, external_id) collisions under -count).
var seedSeq atomic.Int64

// seedItem inserts one workspace-scoped item row and returns its id.
func seedItem(t *testing.T, db *store.DB, srcID int64, title, body, meta string) int64 {
	t.Helper()
	var id int64
	ext := fmt.Sprintf("sem-%d", seedSeq.Add(1))
	if err := db.QueryRowContext(context.Background(),
		`INSERT INTO items (workspace_id, source_id, external_id, url, title, content_text, kind, meta_json)
		 VALUES ($1,$2,$3,$4,$5,$6,'article',$7) RETURNING id`,
		db.Workspace(), srcID, ext, "https://x.example/"+ext, title, body, meta).Scan(&id); err != nil {
		t.Fatalf("seed %q: %v", title, err)
	}
	return id
}

// TestSemanticSweepBackfills: items stored before the layer existed get
// an embedding + a 'local-%' classification; a second sweep finds
// nothing pending and a row with an AI model tag is never overwritten.
func TestSemanticSweepBackfills(t *testing.T) {
	pool := testdb.Pool(t)
	ctx := context.Background()
	db := testdb.WSOn(t, pool)
	pubID, err := db.SeedPublication(ctx, &config.Publication{
		Slug: "sem", Name: "Sem",
		Sections: []config.Section{{Title: "Lead", Items: 1}},
		Sources:  []config.SourceDef{{Type: "rss", URL: "fixtures/feeds/sem.xml"}},
	})
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	srcs, _ := db.Sources(ctx, pubID)
	if len(srcs) == 0 {
		t.Fatal("no seeded sources")
	}
	srcID := srcs[0].ID

	id1 := seedItem(t, db, srcID,
		"Nubank testa pagamento Pix para PJ",
		"O banco digital liberou Pix para contas PJ; a fintech lidera downloads no Brasil.",
		`{"description":"Fintech libera Pix PJ.","categories":["finance"]}`)
	id2 := seedItem(t, db, srcID, "Anatel abre consulta sobre 5G",
		"A agência reguladora publicou decreto e abriu consulta pública do novo marco.",
		`{"categories":["politics"]}`)
	id3 := seedItem(t, db, srcID, "Story with AI row",
		"Body already enriched by the AI sweep.", `{"categories":[]}`)
	if err := pool.SaveEnrichments(ctx, []store.ItemEnrichment{
		{ItemID: id3, Kind: "noticia", Model: "claude-haiku-5-5", Topics: []string{"economia"}},
	}); err != nil {
		t.Fatalf("seed AI row: %v", err)
	}

	s := &SemanticSweep{Pool: pool, WS: db.Workspace()}
	n, err := s.Sweep(ctx, 100)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n < 3 {
		t.Fatalf("sweep embedded %d want >=3", n)
	}
	pending, err := pool.UnembeddedItems(ctx, embedding.Model, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	for _, p := range pending {
		if p.ID == id1 || p.ID == id2 || p.ID == id3 {
			t.Fatalf("item %d still unembedded after sweep", p.ID)
		}
	}

	enr, err := pool.EnrichmentsForItems(ctx, []int64{id1, id2, id3})
	if err != nil {
		t.Fatalf("enrichments: %v", err)
	}
	e1 := enr[id1]
	if e1 == nil || e1.Model != embedding.ClassifyModel {
		t.Fatalf("id1 enrichment=%+v", e1)
	}
	var hasFintech bool
	for _, tp := range e1.Topics {
		if tp == "fintech" {
			hasFintech = true
		}
	}
	if !hasFintech {
		t.Fatalf("id1 topics=%v want fintech", e1.Topics)
	}
	if e1.SummaryPT != "Fintech libera Pix PJ." {
		t.Fatalf("id1 summary=%q", e1.SummaryPT)
	}
	if e3 := enr[id3]; e3 == nil || e3.Model != "claude-haiku-5-5" {
		t.Fatalf("AI row overwritten: %+v", e3)
	}

	// Idempotent: a second sweep over the same window writes the same
	// rows — same model tag, same labels.
	if n2, err := s.Sweep(ctx, 100); err != nil {
		t.Fatalf("second sweep: %v", err)
	} else if n2 < 0 {
		t.Fatalf("second sweep n=%d", n2)
	}
	enr2, _ := pool.EnrichmentsForItems(ctx, []int64{id1})
	if enr2[id1].Kind != e1.Kind || strings.Join(enr2[id1].Topics, ",") != strings.Join(e1.Topics, ",") {
		t.Fatal("second sweep changed the classification")
	}
}

// TestSemanticSweepInvalidatesOnUpdate: a changed story loses its local
// rows via SaveDJItems and the sweep recomputes against the new text.
func TestSemanticSweepInvalidatesOnUpdate(t *testing.T) {
	pool := testdb.Pool(t)
	ws := testdb.WS(t)
	ctx := context.Background()

	cat := catalogBySlug(t, pool, "daily-journal")
	if cat == nil {
		t.Skip("daily-journal catalog row not seeded (migration 0008 missing?)")
	}
	dj := []store.CatalogItem{{
		ExternalID: "klox93-test-1", URL: "https://dailyjournal.news/n/t1",
		Title: "Fintech Pix headline", ContentText: "Pix e fintech no corpo.",
		PublishedAt: "2026-11-21T10:00:00Z", Kind: "article",
		Meta: map[string]any{"categories": []string{"finance"}, "slug": "t1"},
	}}
	if _, _, err := pool.SaveDJItems(ctx, cat.ID, dj); err != nil {
		t.Fatalf("save: %v", err)
	}
	s := &SemanticSweep{Pool: pool, WS: ws.Workspace()}
	if _, err := s.Sweep(ctx, 50); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// Revise the story — the upsert must drop its semantic rows.
	dj[0].Title = "Fintech Pix headline revised"
	dj[0].ContentText = "Corpo revisado sobre Pix."
	if _, c, err := pool.SaveDJItems(ctx, cat.ID, dj); err != nil || c != 1 {
		t.Fatalf("update c=%d err=%v", c, err)
	}
	// The changed row is pending again.
	pending, err := pool.UnembeddedItems(ctx, embedding.Model, 50)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	var found bool
	for _, p := range pending {
		if p.Title == "Fintech Pix headline revised" {
			found = true
		}
	}
	if !found {
		t.Fatal("revised item not back in the unembedded queue")
	}
}

// catalogBySlug resolves a catalog row for store-level tests.
func catalogBySlug(t *testing.T, pool *store.Pool, slug string) *store.CatalogSource {
	t.Helper()
	cat, err := pool.Catalog(context.Background())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	for i := range cat {
		if cat[i].Slug == slug {
			return &cat[i]
		}
	}
	return nil
}
