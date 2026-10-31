-- v5: feeds have a category and are managed in the web dashboard
ALTER TABLE news_sources ADD COLUMN IF NOT EXISTS category   TEXT NOT NULL DEFAULT 'general';
ALTER TABLE news_sources ADD COLUMN IF NOT EXISTS enabled    BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE news_sources ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE news_sources ADD COLUMN IF NOT EXISTS last_items INT NOT NULL DEFAULT 0;
ALTER TABLE news_sources ADD COLUMN IF NOT EXISTS last_fetch TIMESTAMPTZ;
ALTER TABLE news_cache ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'general';
ALTER TABLE news_cache ADD COLUMN IF NOT EXISTS link     TEXT NOT NULL DEFAULT '';
ALTER TABLE news_cache ADD COLUMN IF NOT EXISTS alert    BOOLEAN NOT NULL DEFAULT FALSE;
-- Socket.dev: supply chain compromises in npm, PyPI, extensions and tooling
INSERT INTO news_sources(url, name, category) VALUES ('https://socket.dev/api/blog/feed.atom', 'Socket', 'security')
  ON CONFLICT (url) DO UPDATE SET category='security', name='Socket';
