-- KLOX-163: per-radar editoria selection. The editorias column records
-- the editorial desks the creator picked (comma-separated keys —
-- tecnologia, fintech, mercado, politica, carreira, …). The list is the
-- auditable record behind the Radar's fontes screen: removing an
-- editoria unpicks its catalog sources (DeleteSource semantics — the
-- shared items, stories and past issues keep their rows; only the pick
-- goes away), so removed desks stop feeding the next collect/rank
-- cycle while history stays. Created radars backfill the column at
-- creation; older rows start empty and read as "no desk declared".
ALTER TABLE publications ADD COLUMN editorias TEXT NOT NULL DEFAULT '';
