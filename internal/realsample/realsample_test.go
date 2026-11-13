package realsample_test

// The recorded real run replays end to end: captured feeds in, recorded
// Anthropic replies out — proving the artifacts shipped in the binary are
// complete and self-consistent.
import (
	"context"
	"strconv"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/llm"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/realsample"
	"github.com/sthefanocordeiro/kloxus/internal/sources"
	"github.com/sthefanocordeiro/kloxus/internal/testdb"
)

func TestRecordedRunReplays(t *testing.T) {
	ctx := context.Background()
	db := testdb.WS(t)
	def, err := realsample.Def()
	if err != nil {
		t.Fatalf("realsample.Def: %v", err)
	}
	id, err := db.SeedPublication(ctx, def)
	if err != nil {
		t.Fatalf("SeedPublication: %v", err)
	}
	pub, err := db.GetPublication(ctx, strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	runner := &pipeline.Runner{
		DB:           db,
		LLM:          llm.Replay{FS: realsample.FS, Dir: "llm"},
		Fetcher:      sources.CapturedFetcher{FS: realsample.FS},
		TriageModel:  "claude-haiku-5-5",
		DraftModel:   "claude-sonnet-5-5",
		MaxDeepDives: 3,
	}
	res, err := runner.Run(ctx, pub, t.TempDir())
	if err != nil {
		t.Fatalf("replay run: %v", err)
	}
	if res.IssueID == 0 {
		t.Fatal("replay produced no issue")
	}
	iss, err := db.LatestIssue(ctx, pub.ID)
	if err != nil {
		t.Fatalf("LatestIssue: %v", err)
	}
	if iss.BodyMD == "" {
		t.Fatal("replayed issue has no body")
	}
}

func TestSamplePayloadLoads(t *testing.T) {
	if len(realsample.Sample()) == 0 {
		t.Fatal("embedded sample.json is missing or empty")
	}
}
