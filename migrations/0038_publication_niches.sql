-- KLOX-274: editoria selections are explicit creator state, not text
-- inferred from the ranking topic. The topic still carries selected
-- editoria labels as ranking vocabulary, but this table is the only
-- source of truth for the chips shown and edited on the Radar screen.
CREATE TABLE IF NOT EXISTS publication_niches (
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  publication_id BIGINT NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
  niche          TEXT NOT NULL,
  PRIMARY KEY (publication_id, niche)
);
CREATE INDEX IF NOT EXISTS idx_publication_niches_workspace
  ON publication_niches(workspace_id, publication_id);

-- Upgrade path: before this table existed, onboarding folded each picked
-- editoria's canonical label into publications.topic as a comma-separated
-- part. Recover only exact comma parts. A label embedded in a larger
-- free-text theme is the creator's prose, not evidence that the chip was
-- picked (the QA reproducer that exposed the ambiguous encoding).
INSERT INTO publication_niches (workspace_id, publication_id, niche)
SELECT p.workspace_id, p.id, n.niche
FROM publications p
JOIN (VALUES
  ('politica', 'política'),
  ('economia', 'economia'),
  ('mercado-financeiro', 'mercado financeiro e investimentos'),
  ('financas-pessoais', 'finanças pessoais'),
  ('negocios', 'negócios e empresas'),
  ('startups', 'startups e inovação'),
  ('carreira', 'carreira e trabalho'),
  ('tecnologia', 'tecnologia'),
  ('agronegocio', 'agronegócio'),
  ('internacional', 'internacional'),
  ('brasil-cidades', 'Brasil e cidades'),
  ('justica-direito', 'justiça e direito'),
  ('saude', 'saúde'),
  ('educacao', 'educação'),
  ('ciencia', 'ciência'),
  ('meio-ambiente', 'meio ambiente e sustentabilidade'),
  ('cultura', 'cultura e entretenimento'),
  ('estilo-vida', 'estilo de vida e consumo')
) AS n(niche, label) ON EXISTS (
  SELECT 1
  FROM regexp_split_to_table(p.topic, ',') AS parts(part)
  WHERE lower(btrim(parts.part)) = lower(n.label)
)
ON CONFLICT DO NOTHING;

COMMENT ON TABLE publication_niches IS
  'Explicit editoria chip selections for each publication/radar; never derive this creator state by substring matching publications.topic.';
