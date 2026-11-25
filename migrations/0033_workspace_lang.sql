-- KLOX-232: the interface language follows the account — a magic link
-- opened on a PT browser kept dropping an EN user back to PT. '' means
-- "no choice saved yet": the client falls back to localStorage, then
-- navigator.language.
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS ui_lang TEXT NOT NULL DEFAULT '';
