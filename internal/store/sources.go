package store

import (
	"context"
	"database/sql"
	"time"
)

// DeleteSource removes one source from a publication. Collected items go
// with it — stories and past issues keep their own rows, only the raw
// fetch links are dropped.
func (db *DB) DeleteSource(ctx context.Context, pubID, sourceID int64) error {
	tx, err := db.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM story_items WHERE workspace_id = $1 AND item_id IN (SELECT id FROM items WHERE workspace_id = $1 AND source_id = $2)`,
		`DELETE FROM items WHERE workspace_id = $1 AND source_id = $2`,
	} {
		if _, err := tx.ExecContext(ctx, q, db.ws, sourceID); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM sources WHERE workspace_id = $1 AND id = $2 AND publication_id = $3`, db.ws, sourceID, pubID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// ItemsPerWeek counts items collected from a source in the last 7 days.
// Catalog-bound sources count the shared items their catalog row pulled.
func (db *DB) ItemsPerWeek(ctx context.Context, sourceID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM items i
		WHERE i.fetched_ts > $3 AND (
			(i.workspace_id = $1 AND i.source_id = $2)
			OR i.catalog_id = (SELECT catalog_id FROM sources
			                   WHERE workspace_id = $1 AND id = $2 AND catalog_id IS NOT NULL))`,
		db.ws, sourceID, time.Now().Add(-7*24*time.Hour).UTC()).Scan(&n)
	return n, err
}

// Source is a stored source row. CatalogID links the row to the global
// catalog pick it mirrors (0 for fixture/custom sources).
type Source struct {
	ID          int64
	Name        string
	Type        string
	URL         string
	CatalogID   int64
	Policy      string // catalog policy: launch | signal | hold | blocked
	LastFetchAt sql.NullString
	LastStatus  sql.NullString
}

const sourceCols = `s.id, s.name, s.type, s.url, s.catalog_id,
	COALESCE(c.policy, ''), s.last_fetch_at, s.last_status`

// Sources lists a publication's sources.
func (db *DB) Sources(ctx context.Context, pubID int64) ([]Source, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+sourceCols+` FROM sources s LEFT JOIN source_catalog c ON c.id = s.catalog_id
		 WHERE s.workspace_id = $1 AND s.publication_id = $2 ORDER BY s.id`, db.ws, pubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var s Source
		var catalogID sql.NullInt64
		if err := rows.Scan(&s.ID, &s.Name, &s.Type, &s.URL, &catalogID, &s.Policy, &s.LastFetchAt, &s.LastStatus); err != nil {
			return nil, err
		}
		s.CatalogID = catalogID.Int64
		out = append(out, s)
	}
	return out, rows.Err()
}

// SourceNames maps source id to outlet name for a publication.
func (db *DB) SourceNames(ctx context.Context, pubID int64) (map[int64]string, error) {
	srcs, err := db.Sources(ctx, pubID)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, s := range srcs {
		if s.Name != "" {
			names[s.ID] = s.Name
		}
	}
	return names, nil
}

// AddCatalogSource registers a catalog pick on a publication: the row
// mirrors the catalog's name/url so display reads stay local, and the
// catalog_id link is what the editorial run filters shared items by.
func (db *DB) AddCatalogSource(ctx context.Context, pubID int64, cs *CatalogSource) (int64, error) {
	typ := cs.Kind
	if typ == "api" {
		typ = "rss" // the stored type drives display badges only
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sources (workspace_id, publication_id, type, url, name, catalog_id)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (publication_id, url) DO UPDATE SET catalog_id = excluded.catalog_id`,
		db.ws, pubID, typ, cs.URL, cs.Name, cs.ID); err != nil {
		return 0, err
	}
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM sources WHERE workspace_id = $1 AND publication_id = $2 AND url = $3`, db.ws, pubID, cs.URL).Scan(&id)
	return id, err
}

// AddSource registers a new source on a publication.
func (db *DB) AddSource(ctx context.Context, pubID int64, typ, url, name string) (int64, error) {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sources (workspace_id, publication_id, type, url, name) VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (publication_id, url) DO UPDATE SET
		   name = CASE WHEN excluded.name != '' THEN excluded.name ELSE sources.name END`,
		db.ws, pubID, typ, url, name); err != nil {
		return 0, err
	}
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM sources WHERE workspace_id = $1 AND publication_id = $2 AND url = $3`, db.ws, pubID, url).Scan(&id)
	return id, err
}

// SetSourceMonitor records monitor provider state on a source.
func (db *DB) SetSourceMonitor(ctx context.Context, sourceID int64, provider, monitorID, status string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE sources SET monitor_provider = $1, monitor_id = $2, monitor_status = $3 WHERE workspace_id = $4 AND id = $5`,
		provider, monitorID, status, db.ws, sourceID)
	return err
}

// SourceMonitor returns monitor fields for a source.
func (db *DB) SourceMonitor(ctx context.Context, sourceID int64) (provider, monitorID, status string, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT monitor_provider, monitor_id, monitor_status FROM sources WHERE workspace_id = $1 AND id = $2`,
		db.ws, sourceID).Scan(&provider, &monitorID, &status)
	return
}

// FindSourceByMonitor resolves a source in this workspace by provider
// monitor id — the local-mode variant of the pool lookup.
func (db *DB) FindSourceByMonitor(ctx context.Context, monitorID string) (*Source, error) {
	var s Source
	err := db.QueryRowContext(ctx,
		`SELECT id, type, url, last_fetch_at, last_status FROM sources WHERE workspace_id = $1 AND monitor_id = $2`,
		db.ws, monitorID).Scan(&s.ID, &s.Type, &s.URL, &s.LastFetchAt, &s.LastStatus)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// MonitorSource is a source row plus its workspace — the global lookup
// the Firecrawl webhook needs, since monitor ids cross tenant
// boundaries by nature.
type MonitorSource struct {
	Source
	WorkspaceID string
}

// FindSourceByMonitor resolves a source from a provider monitor id
// across all workspaces — monitor ids are globally unique, and the
// webhook caller carries no tenant context.
func (p *Pool) FindSourceByMonitor(ctx context.Context, monitorID string) (*MonitorSource, error) {
	var s MonitorSource
	err := p.db.QueryRowContext(ctx,
		`SELECT id, workspace_id, type, url, last_fetch_at, last_status FROM sources WHERE monitor_id = $1`,
		monitorID).Scan(&s.ID, &s.WorkspaceID, &s.Type, &s.URL, &s.LastFetchAt, &s.LastStatus)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
