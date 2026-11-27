-- KLOX-152: Brazilian news portals (owner decision, 2026-11-23). Each
-- has a stable public RSS feed and no blanket robots Disallow for it
-- (checked 2026-11-23). Added as 'signal' / body_mode=links: title +
-- URL only, never stored as evidence — the conservative reading of the
-- publishers' terms until each is cleared for full text. Promote to
-- 'launch' only after a terms review. Not added: Estadão (feed only via
-- an undocumented full-text endpoint), O Globo and R7 (no working feed),
-- UOL (15-item ISO-8859-1 feed, terms unreviewed), Canaltech/TecMundo
-- (stay blocked/hold).
INSERT INTO source_catalog
  (slug, name, kind, url, feed_url, policy, domain, crawl_delay_ms, poll_minutes, body_mode, note) VALUES
  ('g1', 'g1', 'rss', 'https://g1.globo.com', 'https://g1.globo.com/rss/g1/',
   'signal', 'g1.globo.com', 0, 30, 'links', 'Geral. Links only until terms are reviewed.'),
  ('folha', 'Folha de S.Paulo', 'rss', 'https://www.folha.uol.com.br', 'https://feeds.folha.uol.com.br/emcimadahora/rss091.xml',
   'signal', 'feeds.folha.uol.com.br', 0, 30, 'links', 'Geral. Links only until terms are reviewed.'),
  ('valor', 'Valor Econômico', 'rss', 'https://valor.globo.com', 'https://valor.globo.com/rss/valor/',
   'signal', 'valor.globo.com', 0, 30, 'links', 'Economia. Links only until terms are reviewed.'),
  ('cnn-brasil', 'CNN Brasil', 'rss', 'https://www.cnnbrasil.com.br', 'https://www.cnnbrasil.com.br/feed/',
   'signal', 'www.cnnbrasil.com.br', 0, 30, 'links', 'Geral. Links only until terms are reviewed.'),
  ('metropoles', 'Metrópoles', 'rss', 'https://www.metropoles.com', 'https://www.metropoles.com/feed',
   'signal', 'www.metropoles.com', 0, 30, 'links', 'Geral. Links only until terms are reviewed.')
ON CONFLICT (slug) DO NOTHING;
