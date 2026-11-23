-- The workspace owner's identity: an e-mail confirmed through a magic
-- link before a Radar can be created. One row per workspace database.
-- launch_invite marks the owner for the definitive-launch invitation.
CREATE TABLE owner_identity (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  email         TEXT NOT NULL DEFAULT '',
  confirmed_at  TEXT NOT NULL DEFAULT '',
  launch_invite INTEGER NOT NULL DEFAULT 0,
  pending_email TEXT NOT NULL DEFAULT '',
  pending_nonce TEXT NOT NULL DEFAULT '',
  pending_exp   TEXT NOT NULL DEFAULT ''
);
