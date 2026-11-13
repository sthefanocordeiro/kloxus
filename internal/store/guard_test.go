package store_test

// Durable-ledger round trips on real Postgres (skipped without
// DATABASE_URL, like the other store tests).
import (
	"context"
	"testing"

	"github.com/sthefanocordeiro/kloxus/internal/testdb"
)

func TestLLMLedgerRoundTrip(t *testing.T) {
	pool := testdb.Pool(t)
	ctx := context.Background()
	ws := "ledger-test-ws"

	beforeRuns, err := pool.LLMRunsSince(ctx, ws, "2000-01-01 00:00:00")
	if err != nil {
		t.Fatalf("LLMRunsSince: %v", err)
	}
	beforeSpend, err := pool.LLMSpendSince(ctx, "2000-01-01 00:00:00")
	if err != nil {
		t.Fatalf("LLMSpendSince: %v", err)
	}

	if err := pool.LLMRunStart(ctx, ws); err != nil {
		t.Fatalf("LLMRunStart: %v", err)
	}
	if err := pool.LLMCall(ctx, ws, "claude-haiku-5-5", 100, 50, 0.001); err != nil {
		t.Fatalf("LLMCall: %v", err)
	}

	runs, err := pool.LLMRunsSince(ctx, ws, "2000-01-01 00:00:00")
	if err != nil {
		t.Fatalf("LLMRunsSince: %v", err)
	}
	if runs < beforeRuns+1 {
		t.Fatalf("runs=%d, want at least %d", runs, beforeRuns+1)
	}
	// The far-future 'since' excludes today's row — the window works.
	if n, err := pool.LLMRunsSince(ctx, ws, "2999-01-01 00:00:00"); err != nil || n != 0 {
		t.Fatalf("future-window runs=%d err=%v, want 0", n, err)
	}
	spend, err := pool.LLMSpendSince(ctx, "2000-01-01 00:00:00")
	if err != nil {
		t.Fatalf("LLMSpendSince: %v", err)
	}
	// The ledger is shared: other tests may add calls concurrently, so
	// the delta is a lower bound, never an exact match. SUM(numeric) →
	// float64 loses a hair of precision, so allow a small epsilon.
	if spend < beforeSpend+0.001-1e-9 {
		t.Fatalf("spend=%f, want at least %f", spend, beforeSpend+0.001)
	}
}

func TestAppSettingsRoundTrip(t *testing.T) {
	pool := testdb.Pool(t)
	ctx := context.Background()
	if _, err := pool.AppSetting(ctx, "test_missing_key"); err == nil {
		t.Fatal("missing key should error")
	}
	if err := pool.SetAppSetting(ctx, "test_key", "1"); err != nil {
		t.Fatalf("SetAppSetting: %v", err)
	}
	v, err := pool.AppSetting(ctx, "test_key")
	if err != nil || v != "1" {
		t.Fatalf("AppSetting=%q %v", v, err)
	}
	if err := pool.SetAppSetting(ctx, "test_key", "0"); err != nil {
		t.Fatalf("SetAppSetting update: %v", err)
	}
	if v, _ := pool.AppSetting(ctx, "test_key"); v != "0" {
		t.Fatalf("AppSetting after update=%q", v)
	}
}
