-- SQLite schema for the article source of truth.
-- STRICT tables reject the loose typing that a Postgres adapter would also
-- reject, keeping the two dialects honest. The stable hot axes (publish date,
-- author, section) are indexed columns; topics are normalized into a join table
-- so /topic/<slug> navigation is an indexed lookup. The open-ended tail lives in
-- the metadata JSON column.

CREATE TABLE IF NOT EXISTS articles (
  id           INTEGER PRIMARY KEY,
  slug         TEXT NOT NULL UNIQUE,
  title        TEXT NOT NULL,
  body         TEXT NOT NULL,
  author       TEXT NOT NULL,
  section      TEXT NOT NULL,
  published_at TEXT NOT NULL,
  content_hash TEXT NOT NULL UNIQUE,
  metadata     TEXT NOT NULL DEFAULT '{}',
  created_at   TEXT NOT NULL
) STRICT;

CREATE INDEX IF NOT EXISTS idx_articles_published_at ON articles (published_at);
CREATE INDEX IF NOT EXISTS idx_articles_author       ON articles (author, published_at);
CREATE INDEX IF NOT EXISTS idx_articles_section      ON articles (section, published_at);

CREATE TABLE IF NOT EXISTS article_topics (
  article_id INTEGER NOT NULL REFERENCES articles (id) ON DELETE CASCADE,
  topic      TEXT NOT NULL,
  PRIMARY KEY (article_id, topic)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_article_topics_topic ON article_topics (topic, article_id);
