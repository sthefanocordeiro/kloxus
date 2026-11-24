package store

// Ranking feedback and section vectors (KLOX-112, epic KLOX-107 G5).
// Accept/reject/swap fold the decided story's vector into the
// publication's incremental Rocchio means; section topics get their own
// cached vectors. The math lives in internal/rank.

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/sthefanocordeiro/kloxus/internal/rank"
)

// Feedback kinds.
const (
	FeedbackAccept = "accept"
	FeedbackReject = "reject"
	// FeedbackUndoReject removes a previously folded reject.
	FeedbackUndoReject = "undo_reject"
)

const maxFeedbackMembers = 12

// storyVector is the normalized mean of a story's member vectors for
// model (nil when the story was rebuilt away or has no embeddings).
func (db *DB) storyVector(ctx context.Context, storyID int64, model string) ([]float32, error) {
	rows, err := db.db.QueryContext(ctx,
		`SELECT e.embedding::text FROM story_items si
		 JOIN stories s ON s.id = si.story_id
		 JOIN item_embeddings e ON e.item_id = si.item_id AND e.model = $2
		 WHERE si.story_id = $1 AND s.workspace_id = $3 LIMIT $4`,
		storyID, model, db.ws, maxFeedbackMembers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vs [][]float32
	for rows.Next() {
		var lit string
		if err := rows.Scan(&lit); err != nil {
			return nil, err
		}
		if v := ParseVector(lit); v != nil {
			vs = append(vs, v)
		}
	}
	return rank.Mean(vs, 0), rows.Err()
}

// ApplyFeedback folds one decision on a story into the publication's
// accepted/rejected mean and count. It is a silent no-op when the story
// has no vectors (embeddings off, story already rebuilt): feedback must
// never fail a decision.
func (db *DB) ApplyFeedback(ctx context.Context, pubID, storyID int64, model, kind string) error {
	if model == "" || model == "off" {
		return nil
	}
	v, err := db.storyVector(ctx, storyID, model)
	if err != nil || v == nil {
		return err
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cast := ""
	if db.vectorOK(ctx) {
		cast = "::vector"
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO publication_profiles (publication_id)
		 SELECT $1 WHERE EXISTS (SELECT 1 FROM publications WHERE id = $1 AND workspace_id = $2)
		 ON CONFLICT (publication_id) DO NOTHING`, pubID, db.ws); err != nil {
		return err
	}
	var acc, rej sql.NullString
	var na, nr int
	err = tx.QueryRowContext(ctx,
		`SELECT accepted_mean::text, rejected_mean::text, n_accepted, n_rejected
		 FROM publication_profiles WHERE publication_id = $1 FOR UPDATE`, pubID).
		Scan(&acc, &rej, &na, &nr)
	if err == sql.ErrNoRows {
		return nil // publication of another workspace
	}
	if err != nil {
		return err
	}
	am, rm := ParseVector(acc.String), ParseVector(rej.String)
	switch kind {
	case FeedbackAccept:
		am, na = rank.AddToMean(am, na, v), na+1
	case FeedbackReject:
		rm, nr = rank.AddToMean(rm, nr, v), nr+1
	case FeedbackUndoReject:
		if nr == 0 {
			return nil
		}
		rm, nr = rank.RemoveFromMean(rm, nr, v), nr-1
	default:
		return nil
	}
	lit := func(v []float32) any {
		if len(v) == 0 {
			return nil
		}
		return vecLiteral(v)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE publication_profiles SET accepted_mean = $2`+cast+`, rejected_mean = $3`+cast+`,
		   n_accepted = $4, n_rejected = $5, updated_at = now_utc() WHERE publication_id = $1`,
		pubID, lit(am), lit(rm), na, nr); err != nil {
		return err
	}
	return tx.Commit()
}

// FeedbackForItem resolves an issue item to its story and applies kind.
func (db *DB) FeedbackForItem(ctx context.Context, pubID, issueItemID int64, model, kind string) error {
	ii, err := db.GetIssueItem(ctx, issueItemID)
	if err != nil || ii == nil {
		return err
	}
	return db.ApplyFeedback(ctx, pubID, ii.StoryID, model, kind)
}

// FeedbackForStory is ApplyFeedback for a story picked outside an issue
// item (the swap candidate).
func (db *DB) FeedbackForStory(ctx context.Context, pubID, storyID int64, model, kind string) error {
	return db.ApplyFeedback(ctx, pubID, storyID, model, kind)
}

func vecLiteral(v []float32) string {
	b := make([]byte, 0, len(v)*10)
	b = append(b, '[')
	for i, x := range v {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendFloat(b, x)
	}
	return string(append(b, ']'))
}

// SectionProfile is a section's cached topic vector.
type SectionProfile struct {
	Vec       []float32
	TopicHash string
}

// SectionProfiles returns the cached vectors of a publication by section id.
func (db *DB) SectionProfiles(ctx context.Context, pubID int64) (map[string]SectionProfile, error) {
	rows, err := db.db.QueryContext(ctx,
		`SELECT sp.section_id, sp.vec::text, sp.topic_hash FROM section_profiles sp
		 JOIN publications pu ON pu.id = sp.publication_id
		 WHERE sp.publication_id = $1 AND pu.workspace_id = $2`, pubID, db.ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SectionProfile{}
	for rows.Next() {
		var id, hash string
		var lit sql.NullString
		if err := rows.Scan(&id, &lit, &hash); err != nil {
			return nil, err
		}
		out[id] = SectionProfile{Vec: ParseVector(lit.String), TopicHash: hash}
	}
	return out, rows.Err()
}

// SaveSectionProfile upserts one section vector.
func (db *DB) SaveSectionProfile(ctx context.Context, pubID int64, sectionID string, vec []float32, hash string) error {
	cast := ""
	if db.vectorOK(ctx) {
		cast = "::vector"
	}
	_, err := db.db.ExecContext(ctx,
		`INSERT INTO section_profiles (publication_id, section_id, vec, topic_hash)
		 SELECT $1, $2, $3`+cast+`, $4
		 WHERE EXISTS (SELECT 1 FROM publications WHERE id = $1 AND workspace_id = $5)
		 ON CONFLICT (publication_id, section_id) DO UPDATE SET
		   vec = EXCLUDED.vec, topic_hash = EXCLUDED.topic_hash, updated_at = now_utc()`,
		pubID, sectionID, vecLiteral(vec), hash, db.ws)
	return err
}

func appendFloat(b []byte, x float32) []byte {
	return strconv.AppendFloat(b, float64(x), 'g', -1, 32)
}
