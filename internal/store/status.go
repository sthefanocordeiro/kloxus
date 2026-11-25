package store

// Issue lifecycle values for issues.status
// (see migrations/0001_init.sql).
const (
	IssueDraft     = "draft"
	IssueReviewing = "reviewing"
	IssueApproved  = "approved"
	IssueBlocked   = "blocked"
	IssueSent      = "sent"
	IssueScheduled = "scheduled"
)

// Lineup decision values for issue_items.status
// (see migrations/0001_init.sql).
const (
	ItemPending     = "pending"
	ItemApproved    = "approved"
	ItemRejected    = "rejected"
	ItemAlternative = "alternative"
)
