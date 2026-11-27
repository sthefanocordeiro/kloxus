-- Workspace credential rotation (KLOX-279): the workspace cookie is a
-- bearer credential, so logout rotates one server-side epoch. Cookies
-- minted before this column existed carry no epoch and are read as 0.
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS auth_epoch BIGINT NOT NULL DEFAULT 0;
