package store

import (
	"context"
	"fmt"
	"strings"
)

// WorkspaceInventory is the read-only admin view of one workspace: its
// identity, claim state and a count of the rows that tell the old
// (newsletter) model from the new (editor de pauta) one. KLOX-155.
type WorkspaceInventory struct {
	ID                               string
	Label                            string
	CreatedAt                        string
	LastSeenAt                       string
	OwnerEmail                       string // raw; callers must mask before printing
	ClaimedAt                        string
	Publications, CustomPublications int
	PautaDays, Renditions            int
	Readers, Subscriptions           int
	ActiveSubscriptions              int
	PaymentAccounts, Payments        int
	OutboxEmails                     int
	Issues, SentIssues               int
	Runs                             int
}

// Claimed reports whether a human owns the workspace.
func (w WorkspaceInventory) Claimed() bool {
	return w.OwnerEmail != "" || w.ClaimedAt != ""
}

// Workspace classes reported by ClassifyWorkspace.
const (
	WSPreserve     = "preserve"      // never a deletion candidate
	WSNew          = "new-model"     // carries editor-de-pauta artifacts
	WSAmbiguous    = "ambiguous"     // cannot be told apart safely
	WSOldCandidate = "old-candidate" // anonymous legacy residue
)

// ClassifyWorkspace applies the recorded KLOX-155 rule. Order matters:
// protections first, so a workspace is only ever a candidate when no
// protection fires and it shows no new-model artifact.
//
//  1. the "local" demo workspace, claimed workspaces (owner_email or
//     claimed_at), and any workspace with payment, active-subscription,
//     reader or sent-issue data are preserved;
//  2. pauta_days or renditions rows mark the new model;
//  3. a creator-made (custom) publication is ambiguous;
//  4. whatever is left is anonymous legacy residue: seeded publications
//     and legacy artifacts only (or nothing at all).
func ClassifyWorkspace(w WorkspaceInventory) (class, reason string) {
	var why []string
	if w.ID == "local" {
		why = append(why, "local demo workspace")
	}
	if w.Claimed() {
		why = append(why, "claimed / has owner_email")
	}
	if w.PaymentAccounts+w.Payments > 0 {
		why = append(why, "payment data")
	}
	if w.ActiveSubscriptions > 0 {
		why = append(why, "active subscriptions")
	}
	if w.Readers > 0 {
		why = append(why, "readers")
	}
	if w.SentIssues > 0 {
		why = append(why, "sent issues")
	}
	if len(why) > 0 {
		return WSPreserve, strings.Join(why, "; ")
	}
	if w.PautaDays > 0 || w.Renditions > 0 {
		return WSNew, "pauta_days/renditions present"
	}
	if w.CustomPublications > 0 {
		return WSAmbiguous, "creator-made publication, no pauta artifacts"
	}
	if w.Subscriptions > 0 || w.OutboxEmails > 0 {
		return WSAmbiguous, "legacy subscriptions/outbox rows"
	}
	return WSOldCandidate, "anonymous, seeded/empty, no new-model or real data"
}

// WorkspaceInventories lists every workspace with its row counts, oldest
// first. Read-only.
func (p *Pool) WorkspaceInventories(ctx context.Context) ([]WorkspaceInventory, error) {
	c := func(table, where string) string {
		return fmt.Sprintf("(SELECT count(*) FROM %s t WHERE t.workspace_id = w.id%s)", table, where)
	}
	q := `SELECT w.id, w.label, w.created_at, w.last_seen_at, w.owner_email, w.claimed_at,
  ` + c("publications", "") + `,
  ` + c("publications", " AND t.custom") + `,
  ` + c("pauta_days", "") + `,
  ` + c("renditions", "") + `,
  ` + c("readers", "") + `,
  ` + c("subscriptions", "") + `,
  ` + c("subscriptions", " AND t.status = 'active'") + `,
  ` + c("payment_accounts", "") + `,
  ` + c("payments", "") + `,
  ` + c("outbox_emails", "") + `,
  ` + c("issues", "") + `,
  ` + c("issues", " AND t.status = 'sent'") + `,
  ` + c("runs", "") + `
FROM workspaces w ORDER BY w.created_at, w.id`
	rows, err := p.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkspaceInventory
	for rows.Next() {
		var w WorkspaceInventory
		if err := rows.Scan(&w.ID, &w.Label, &w.CreatedAt, &w.LastSeenAt, &w.OwnerEmail, &w.ClaimedAt,
			&w.Publications, &w.CustomPublications, &w.PautaDays, &w.Renditions,
			&w.Readers, &w.Subscriptions, &w.ActiveSubscriptions,
			&w.PaymentAccounts, &w.Payments, &w.OutboxEmails,
			&w.Issues, &w.SentIssues, &w.Runs); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
