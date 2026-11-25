-- LLM spend guards (D32/D36): a durable ledger of real model calls and
-- run starts, plus a global settings table for the persisted kill
-- switch. Rows must NOT cascade on workspace delete/reset: a reset
-- workspace would otherwise get its spend budget back, and the global
-- daily cap depends on keeping every recorded call.
CREATE TABLE app_settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT (now_utc())
);

CREATE TABLE llm_ledger (
  id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id  TEXT NOT NULL DEFAULT '',
  kind          TEXT NOT NULL,              -- run_start | call
  model         TEXT NOT NULL DEFAULT '',
  input_tokens  INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  cost_usd      DOUBLE PRECISION NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL DEFAULT (now_utc())
);
CREATE INDEX idx_llm_ledger_ws_day ON llm_ledger(workspace_id, created_at);
CREATE INDEX idx_llm_ledger_day ON llm_ledger(created_at);
