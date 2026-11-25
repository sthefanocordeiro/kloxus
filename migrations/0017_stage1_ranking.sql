-- KLOX-109 (ranking engine G2): stage-1 verdict persisted on the story
-- so the board filter can hide off-topic / English suggestions without
-- recomputing. rel is the cosine (or scaled FTS) relevance; the hidden
-- flag is '' (visible), 'off_topic' or 'english'.
ALTER TABLE stories ADD COLUMN IF NOT EXISTS rel REAL;
ALTER TABLE stories ADD COLUMN IF NOT EXISTS stage1_hidden TEXT NOT NULL DEFAULT '';
