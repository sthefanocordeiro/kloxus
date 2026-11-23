package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// cmdAdmin is the operator surface (KLOX-155). It runs inside the deployed
// binary so no database credential leaves the app. Only the read-only
// `workspaces list` exists; deletion ships separately after the inventory
// is reviewed.
func cmdAdmin(args []string) error {
	if len(args) < 2 || args[0] != "workspaces" || args[1] != "list" {
		return errors.New("usage: kloxus admin workspaces list [--json]")
	}
	fs := flag.NewFlagSet("admin workspaces list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// store.Open directly: openDB would create/touch the "local" workspace,
	// and an inventory must not write.
	pool, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return printWorkspaceInventory(context.Background(), pool, *asJSON)
}

func printWorkspaceInventory(ctx context.Context, pool *store.Pool, asJSON bool) error {
	ws, err := pool.WorkspaceInventories(ctx)
	if err != nil {
		return err
	}
	type row struct {
		ID, Label, Created, LastSeen, Owner, Class, Reason string
		Counts                                             map[string]int
	}
	var rows []row
	tally := map[string]int{}
	for _, w := range ws {
		class, reason := store.ClassifyWorkspace(w)
		tally[class]++
		rows = append(rows, row{
			ID: w.ID, Label: w.Label, Created: w.CreatedAt, LastSeen: w.LastSeenAt,
			Owner: maskEmail(w.OwnerEmail), Class: class, Reason: reason,
			Counts: map[string]int{
				"publications": w.Publications, "custom_publications": w.CustomPublications,
				"pauta_days": w.PautaDays, "renditions": w.Renditions,
				"readers": w.Readers, "subscriptions": w.Subscriptions,
				"active_subscriptions": w.ActiveSubscriptions,
				"payment_accounts":     w.PaymentAccounts, "payments": w.Payments,
				"outbox_emails": w.OutboxEmails, "issues": w.Issues,
				"sent_issues": w.SentIssues, "runs": w.Runs,
			},
		})
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"total": len(rows), "by_class": tally, "workspaces": rows})
	}
	fmt.Printf("%-14s %-14s %-20s %-20s %-14s %s\n", "id", "class", "created", "last_seen", "owner", "pubs/pauta/rend/readers/subs/sent  reason")
	for _, r := range rows {
		c := r.Counts
		fmt.Printf("%-14s %-14s %-20s %-20s %-14s %d/%d/%d/%d/%d/%d  %s\n",
			r.ID, r.Class, r.Created, r.LastSeen, r.Owner,
			c["publications"], c["pauta_days"], c["renditions"], c["readers"], c["subscriptions"], c["sent_issues"], r.Reason)
	}
	fmt.Printf("\ntotal %d — preserve %d, new-model %d, ambiguous %d, old-candidate %d (read-only; nothing was changed)\n",
		len(rows), tally[store.WSPreserve], tally[store.WSNew], tally[store.WSAmbiguous], tally[store.WSOldCandidate])
	return nil
}

// maskEmail keeps just enough to tell owners apart without printing the
// address into logs or issue comments.
func maskEmail(e string) string {
	at := strings.LastIndex(e, "@")
	if at < 1 {
		return e
	}
	return e[:1] + "***" + e[at:]
}
