-- KLOX-127: public pre-launch waitlist for the pivot. Global (no
-- workspace): the landing visitor has no session. Emails are stored
-- lower-cased and trimmed by the handler; the unique index on lower()
-- keeps that true even for writes that bypass it (plain TEXT rather
-- than citext so no extension privilege is needed on managed Postgres).
CREATE TABLE IF NOT EXISTS waitlist (
    id         BIGSERIAL PRIMARY KEY,
    email      TEXT NOT NULL,
    source     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS waitlist_email_lower_idx ON waitlist (lower(email));
