-- KLOX-154: one daily pauta mail per recipient, not per workspace. The
-- ledger key (pauta/<workspace>/<day>) is per workspace, so an owner
-- holding several claimed radars got one identical mail per radar as
-- the sweep walked them. This global claim makes the send once per
-- (recipient, São Paulo day).
CREATE TABLE pauta_mail_days (
  email      TEXT NOT NULL,
  day        TEXT NOT NULL,                -- 'YYYY-MM-DD' in America/Sao_Paulo
  claimed_at TEXT NOT NULL DEFAULT (now_utc()),
  PRIMARY KEY (email, day)
);
