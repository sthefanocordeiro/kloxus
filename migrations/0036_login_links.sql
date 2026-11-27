-- Account recovery sign-in links (KLOX-272). Unlike workspace-claim
-- links, these are minted before any session exists and therefore are
-- not tied to the empty workspace a returning visitor would otherwise
-- create. Only the SHA-256 hash is stored; links are single-use and
-- expire in minutes.
CREATE TABLE login_links (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  token_hash TEXT NOT NULL UNIQUE,
  email      TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at    TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (now_utc())
);
CREATE INDEX idx_login_links_email_created ON login_links(email, created_at);
