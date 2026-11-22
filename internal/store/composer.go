package store

// Composer assets (KLOX-66 D3): author-uploaded images served at
// /m/{id}-{token}, and the reusable standard-message library.
import (
	"context"
	"database/sql"
)

// SaveMedia stores an uploaded image inside the workspace and returns
// its id. The caller pairs it with the token to build the public URL.
func (db *DB) SaveMedia(ctx context.Context, pubID int64, token, contentType string, data []byte) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO media (workspace_id, publication_id, token, content_type, data)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		db.ws, pubID, token, contentType, data).Scan(&id)
	return id, err
}

// GetMedia reads an image back for the public /m/ route — the token in
// the URL is the only authorization (unguessable, like claim links).
func (p *Pool) GetMedia(ctx context.Context, id int64, token string) (string, []byte, error) {
	var ct string
	var data []byte
	err := p.db.QueryRowContext(ctx,
		`SELECT content_type, data FROM media WHERE id = $1 AND token = $2`, id, token).
		Scan(&ct, &data)
	return ct, data, err
}

// StandardMessage is one reusable snippet — greeting, footer,
// signature — the author inserts into an edition without retyping.
type StandardMessage struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Body string `json:"body"`
}

// StandardMessages lists a publication's message library, oldest first.
func (db *DB) StandardMessages(ctx context.Context, pubID int64) ([]StandardMessage, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, body FROM standard_messages
		 WHERE workspace_id = $1 AND publication_id = $2 ORDER BY id`, db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StandardMessage{}
	for rows.Next() {
		var m StandardMessage
		if err := rows.Scan(&m.ID, &m.Name, &m.Body); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CreateStandardMessage stores a library entry.
func (db *DB) CreateStandardMessage(ctx context.Context, pubID int64, name, body string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO standard_messages (workspace_id, publication_id, name, body)
		 VALUES ($1,$2,$3,$4) RETURNING id`, db.ws, pubID, name, body).Scan(&id)
	return id, err
}

// DeleteStandardMessage removes a library entry — sql.ErrNoRows when
// it does not belong to the workspace.
func (db *DB) DeleteStandardMessage(ctx context.Context, pubID, mid int64) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM standard_messages WHERE workspace_id = $1 AND publication_id = $2 AND id = $3`,
		db.ws, pubID, mid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
