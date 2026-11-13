package store

// Tenant isolation: every repo method is workspace-scoped, so rows of
// one workspace must be invisible — and immutable — from another, even
// when the attacker knows the victim's row ids (ids are globally unique
// identity values, so "guess the id" is realistic).

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
)

func TestWorkspaceIsolation(t *testing.T) {
	pool := openPool(t)
	a := openOn(t, pool) // victim
	b := openOn(t, pool) // attacker
	ctx := context.Background()

	// --- publications ---
	pubA, err := a.SeedPublication(ctx, testPub())
	if err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if pubs, _ := b.ListPublications(ctx); len(pubs) != 0 {
		t.Fatalf("B sees A's publications: %+v", pubs)
	}
	if _, err := b.GetPublication(ctx, "test-pub"); err != sql.ErrNoRows {
		t.Fatalf("B reads A's pub by slug: %v", err)
	}
	if _, err := b.GetPublication(ctx, strconv.FormatInt(pubA, 10)); err != sql.ErrNoRows {
		t.Fatalf("B reads A's pub by id: %v", err)
	}

	// --- issues + items ---
	issueID, err := a.CreateIssue(ctx, pubA, 1, "s", "body")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if issues, _ := b.ListIssues(ctx, pubA); len(issues) != 0 {
		t.Fatalf("B sees A's issues: %+v", issues)
	}
	if _, err := b.GetIssue(ctx, issueID); err != sql.ErrNoRows {
		t.Fatalf("B reads A's issue by id: %v", err)
	}
	// B cannot mutate A's issue even knowing its id.
	if err := b.SetIssueStatus(ctx, issueID, "sent"); err != nil {
		t.Fatalf("SetIssueStatus: %v", err)
	}
	iss, _ := a.GetIssue(ctx, issueID)
	if iss.Status == "sent" {
		t.Fatal("B changed A's issue status")
	}
	if err := b.UpdateIssue(ctx, issueID, "sent", "hijacked", "{}"); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if body, _ := a.IssueBody(ctx, pubA, 1); body == "hijacked" {
		t.Fatal("B overwrote A's issue body")
	}

	// --- readers/subscribers ---
	ridA, err := a.UpsertReader(ctx, pubA, "a@x.test", "A Reader")
	if err != nil {
		t.Fatalf("UpsertReader: %v", err)
	}
	var visible int
	b.QueryRowContext(ctx, `SELECT count(*) FROM readers WHERE workspace_id = $1`, b.ws).Scan(&visible)
	if visible != 0 {
		t.Fatalf("B sees A's readers: %d", visible)
	}
	if n, _ := b.ReaderCount(ctx, pubA); n != 0 {
		t.Fatalf("B counts A's readers: %d", n)
	}
	// upserting the same email in B's own publication is a different
	// reader, never A's row.
	p2 := testPub()
	p2.Slug = "b-pub"
	pubB, err := b.SeedPublication(ctx, p2)
	if err != nil {
		t.Fatalf("seed B: %v", err)
	}
	ridB, err := b.UpsertReader(ctx, pubB, "a@x.test", "B Reader")
	if err != nil {
		t.Fatalf("UpsertReader B: %v", err)
	}
	if ridB == ridA {
		t.Fatal("B upsert returned A's reader id")
	}
	if name := readerName(t, a, ridA); name != "A Reader" {
		t.Fatalf("A's reader was clobbered: %q", name)
	}

	// --- runs ---
	if err := a.RecordRun(ctx, Run{PublicationID: pubA, Step: "triage"}); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	var runsA, runsB int
	a.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE workspace_id = $1`, a.ws).Scan(&runsA)
	b.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE workspace_id = $1`, b.ws).Scan(&runsB)
	if runsA != 1 || runsB != 0 {
		t.Fatalf("run isolation broken: A=%d B=%d", runsA, runsB)
	}
	// B's cost report only sees its own runs.
	if rows, _ := b.CostReport(ctx, pubA, 10); len(rows) != 0 {
		t.Fatalf("B sees A's runs in CostReport: %+v", rows)
	}

	// --- workspace delete cascades ---
	if err := pool.DeleteWorkspace(ctx, a.ws); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	var orphans int
	pool.db.QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM publications WHERE workspace_id = $1) +
		        (SELECT count(*) FROM issues WHERE workspace_id = $1) +
		        (SELECT count(*) FROM readers WHERE workspace_id = $1)`, a.ws).Scan(&orphans)
	if orphans != 0 {
		t.Fatalf("cascade left %d rows behind", orphans)
	}
}

func readerName(t *testing.T, db *DB, rid int64) string {
	t.Helper()
	var name string
	if err := db.QueryRowContext(context.Background(),
		`SELECT name FROM readers WHERE workspace_id = $1 AND id = $2`, db.ws, rid).Scan(&name); err != nil {
		t.Fatalf("reader name: %v", err)
	}
	return name
}
