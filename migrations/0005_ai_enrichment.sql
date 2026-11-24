-- KLOX-67 AI layer: shared item enrichment, per-story cached research,
-- paid-action counters and the AI-draft marker on issue items. All of it
-- is shared infrastructure (no workspace_id on the enrichment/research
-- tables): enrichment happens once per article for the whole pool and
-- research once per story for every newsletter that picks it.

-- item_enrichment: one row per item, written once and never recomputed.
-- ai_kind is a slug (noticia, analise, tutorial, forum, lancamento,
-- lista, publieditorial, entretenimento, esporte, efemeride, outro);
-- topics_json carries up to five fixed-list tags.
CREATE TABLE IF NOT EXISTS item_enrichment (
  item_id     BIGINT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  ai_kind     TEXT    NOT NULL DEFAULT 'outro',
  promotional BOOLEAN NOT NULL DEFAULT FALSE,
  topics_json TEXT    NOT NULL DEFAULT '[]',
  summary_pt  TEXT    NOT NULL DEFAULT '',
  language    TEXT    NOT NULL DEFAULT '',
  model       TEXT    NOT NULL DEFAULT '',
  enriched_at TEXT    NOT NULL DEFAULT (to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS'))
);
CREATE INDEX IF NOT EXISTS item_enrichment_exclude_idx
  ON item_enrichment (promotional, ai_kind);

-- story_research: the research/draft payload cached per story key (a
-- content hash of the story's member items — see pipeline.ResearchKey).
-- Shared across newsletters; any publication selecting the same story
-- reuses the row. json holds summary, facts, open questions and the
-- pauta draft body.
CREATE TABLE IF NOT EXISTS story_research (
  research_key TEXT PRIMARY KEY,
  json         TEXT NOT NULL,
  model        TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS'))
);

-- paid_actions: the §7.3 counters, kept separate from llm_ledger so the
-- per-edition / per-newsletter caps carry their ref (issue id,
-- publication id). kind is 'research', 'citecheck' or 'rerank'; day is
-- the UTC calendar day.
CREATE TABLE IF NOT EXISTS paid_actions (
  workspace_id TEXT   NOT NULL,
  kind         TEXT   NOT NULL,
  ref_id       BIGINT NOT NULL DEFAULT 0,
  day          TEXT   NOT NULL DEFAULT (to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD')),
  calls        INT    NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, kind, ref_id, day)
);

-- issue_items.ai_draft: the pauta body arrived AI-written; the flag is
-- what the composer marks "Gerado por IA" on and what any author edit
-- clears (the sentence/body writes go through SaveIssueEdit).
ALTER TABLE issue_items
  ADD COLUMN IF NOT EXISTS ai_draft BOOLEAN NOT NULL DEFAULT FALSE;

-- Deals prefilter — KLOX-67 additions to the §6 keyword list:
-- 'preco imbativel', 'black do', 'cupom de desconto', 'frete gratis'
-- ('black da' is already covered). The folded list mirrors
-- internal/store/deals.go — update both together.
UPDATE items SET kind = 'promo'
WHERE kind <> 'promo' AND EXISTS (
  SELECT 1 FROM unnest(ARRAY[
    'preco imbativel', 'black do', 'cupom de desconto', 'frete gratis'
  ]) AS t(term)
  WHERE strpos(
    regexp_replace(
      translate(lower(items.title),
        'áàâãäéèêëíìîïóòôõöúùûüçñ',
        'aaaaaeeeeiiiiooooouuuucn'),
      '[[:space:]]+', ' ', 'g'),
    t.term) > 0
);
