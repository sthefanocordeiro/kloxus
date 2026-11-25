-- KLOX-158 (epic KLOX-156, P0-2 / D9): the catalog grows past technology
-- into economia, negócios, mercado, política, fintech and carreira/RH.
-- Every feed was audited 2026-11-23: public RSS, HTTP 200 with items for
-- the KloxusBot UA, and robots.txt carries no Disallow for the feed path
-- under `User-agent: *`. No paywalled or sponsored-only feeds.
-- All added as 'signal' / body_mode=links (title + URL only, never stored
-- as evidence) — the conservative reading until each publisher's terms
-- are reviewed, same posture as KLOX-152. Agência Brasil and Câmara are
-- public-agency feeds; Agência Brasil asks for a 10 s crawl delay.
-- Not added: Exame (held by publisher), E-Investidor (403), Forbes BR
-- (403), Nexo (paywall), NeoFeed (feed ok but AI-use signals say
-- ai-train=no — kept out until reviewed), Capital Reset (climate desk,
-- off-brief), HBR Brasil (feed is SEO spam today).
INSERT INTO source_catalog
  (slug, name, kind, url, feed_url, policy, domain, crawl_delay_ms, poll_minutes, body_mode, note) VALUES
  ('infomoney', 'InfoMoney', 'rss', 'https://www.infomoney.com.br', 'https://www.infomoney.com.br/feed/',
   'signal', 'www.infomoney.com.br', 0, 30, 'links', 'Economia e mercado. Links only until terms are reviewed.'),
  ('seu-dinheiro', 'Seu Dinheiro', 'rss', 'https://www.seudinheiro.com', 'https://www.seudinheiro.com/feed/',
   'signal', 'www.seudinheiro.com', 0, 30, 'links', 'Economia e mercado. Links only until terms are reviewed.'),
  ('money-times', 'Money Times', 'rss', 'https://www.moneytimes.com.br', 'https://www.moneytimes.com.br/feed/',
   'signal', 'www.moneytimes.com.br', 0, 30, 'links', 'Mercado e negócios. Links only until terms are reviewed.'),
  ('agencia-brasil-economia', 'Agência Brasil — Economia', 'rss', 'https://agenciabrasil.ebc.com.br', 'https://agenciabrasil.ebc.com.br/rss/economia/feed.xml',
   'signal', 'agenciabrasil.ebc.com.br', 10000, 45, 'links', 'Economia (agência pública). 10 s crawl delay.'),
  ('agencia-brasil-politica', 'Agência Brasil — Política', 'rss', 'https://agenciabrasil.ebc.com.br', 'https://agenciabrasil.ebc.com.br/rss/politica/feed.xml',
   'signal', 'agenciabrasil.ebc.com.br', 10000, 45, 'links', 'Política (agência pública). 10 s crawl delay.'),
  ('camara-noticias', 'Câmara dos Deputados — Notícias', 'rss', 'https://www.camara.leg.br', 'https://www.camara.leg.br/noticias/rss/ultimas-noticias',
   'signal', 'www.camara.leg.br', 0, 45, 'links', 'Política/legislativo (órgão público).'),
  ('poder360', 'Poder360', 'rss', 'https://www.poder360.com.br', 'https://www.poder360.com.br/feed/',
   'signal', 'www.poder360.com.br', 0, 30, 'links', 'Política e economia. Links only until terms are reviewed.'),
  ('finsiders', 'Finsiders Brasil', 'rss', 'https://finsiders.com.br', 'https://finsiders.com.br/feed/',
   'signal', 'finsiders.com.br', 0, 45, 'links', 'Fintech, pagamentos e regulação. Links only until terms are reviewed.'),
  ('mundo-rh', 'Mundo RH', 'rss', 'https://www.mundorh.com.br', 'https://www.mundorh.com.br/feed/',
   'signal', 'www.mundorh.com.br', 0, 60, 'links', 'Carreira, RH e gestão de pessoas. Links only until terms are reviewed.'),
  ('rh-portal', 'RH Portal', 'rss', 'https://www.rhportal.com.br', 'https://www.rhportal.com.br/feed/',
   'signal', 'www.rhportal.com.br', 0, 60, 'links', 'Carreira, RH e DP. Links only until terms are reviewed.')
ON CONFLICT (slug) DO NOTHING;
