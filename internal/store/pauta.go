package store

// The daily pauta (KLOX-123): one stored row per radar per São Paulo
// day — the cards the Hoje screen serves plus the 07:00 mail marker.
import "context"

// PautaDay is one radar's stored pauta for a day.
type PautaDay struct {
	CardsJSON string
	BuiltAt   string
	EmailedAt string
}

// SavePautaDay upserts the day's cards for a publication.
func (db *DB) SavePautaDay(ctx context.Context, pubID int64, day, cardsJSON string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO pauta_days (workspace_id, publication_id, day, cards_json, built_at)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (workspace_id, publication_id, day)
		DO UPDATE SET cards_json = EXCLUDED.cards_json, built_at = EXCLUDED.built_at`,
		db.ws, pubID, day, cardsJSON, NowUTC())
	return err
}

// PautaDay loads the stored pauta for (publication, day), or
// sql.ErrNoRows when none was built.
func (db *DB) PautaDay(ctx context.Context, pubID int64, day string) (*PautaDay, error) {
	var p PautaDay
	err := db.QueryRowContext(ctx, `
		SELECT cards_json, built_at, emailed_at FROM pauta_days
		WHERE workspace_id = $1 AND publication_id = $2 AND day = $3`,
		db.ws, pubID, day).Scan(&p.CardsJSON, &p.BuiltAt, &p.EmailedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// DeletePautaDay drops a stored pauta — an interaction that changes the
// ranking (Sim/Não, skip, Escrever, a new calibration deal) makes the
// next Hoje rebuild live.
func (db *DB) DeletePautaDay(ctx context.Context, pubID int64, day string) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM pauta_days WHERE workspace_id = $1 AND publication_id = $2 AND day = $3`,
		db.ws, pubID, day)
	return err
}

// MarkPautaDayEmailed stamps the 07:00 mail as handled for the day —
// sent, refused or skipped (no saved account): one attempt, once.
func (db *DB) MarkPautaDayEmailed(ctx context.Context, pubID int64, day string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE pauta_days SET emailed_at = $4
		WHERE workspace_id = $1 AND publication_id = $2 AND day = $3 AND emailed_at = ''`,
		db.ws, pubID, day, NowUTC())
	return err
}

// ActiveWorkspaces lists workspace ids seen since the given
// (TSUTC-shaped) cutoff — the "radars visited in the last 7 days" the
// daily pauta serves.
func (p *Pool) ActiveWorkspaces(ctx context.Context, since string) ([]string, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id FROM workspaces WHERE last_seen_at >= $1 ORDER BY id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
