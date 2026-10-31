-- Content tables: filled by `dashboard import` from the data/ folder. Import overwrites them.
CREATE TABLE IF NOT EXISTS guitar_plan (
  week        INT PRIMARY KEY,
  lesson      TEXT NOT NULL,
  exercises   JSONB NOT NULL DEFAULT '[]',
  notes       TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS german_items (
  id          SERIAL PRIMARY KEY,
  kind        TEXT NOT NULL CHECK (kind IN ('sentence','word')),
  de          TEXT NOT NULL,
  en          TEXT NOT NULL,
  UNIQUE (kind, de)
);

CREATE TABLE IF NOT EXISTS vocab_items (
  id          SERIAL PRIMARY KEY,
  word        TEXT NOT NULL UNIQUE,
  pos         TEXT NOT NULL DEFAULT '',
  definition  TEXT NOT NULL,
  example     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS books (
  id          SERIAL PRIMARY KEY,
  title       TEXT NOT NULL UNIQUE,
  author      TEXT NOT NULL DEFAULT '',
  pages       INT NOT NULL DEFAULT 0,
  status      TEXT NOT NULL DEFAULT 'next' CHECK (status IN ('reading','next','done')),
  started_on  DATE
);

CREATE TABLE IF NOT EXISTS news_sources (
  url         TEXT PRIMARY KEY,
  name        TEXT NOT NULL DEFAULT ''
);

-- State tables: owned by the backend.
CREATE TABLE IF NOT EXISTS practice_state (
  key          TEXT PRIMARY KEY,          -- guitar | german | vocab | reading
  current_week INT NOT NULL DEFAULT 1,
  started_on   DATE NOT NULL DEFAULT CURRENT_DATE
);

CREATE TABLE IF NOT EXISTS practice_log (
  key   TEXT NOT NULL,
  day   DATE NOT NULL,
  done  BOOLEAN NOT NULL DEFAULT TRUE,
  PRIMARY KEY (key, day)
);

CREATE TABLE IF NOT EXISTS reviews (
  kind          TEXT NOT NULL,            -- german | vocab
  item_id       INT NOT NULL,
  due           DATE NOT NULL DEFAULT CURRENT_DATE,
  interval_days INT NOT NULL DEFAULT 1,
  reviewed      INT NOT NULL DEFAULT 0,
  PRIMARY KEY (kind, item_id)
);

CREATE TABLE IF NOT EXISTS reading_sessions (
  id       SERIAL PRIMARY KEY,
  book_id  INT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  day      DATE NOT NULL DEFAULT CURRENT_DATE,
  pages    INT NOT NULL DEFAULT 0,
  minutes  INT NOT NULL DEFAULT 0
);

-- Caches: filled by the collector from external sources.
CREATE TABLE IF NOT EXISTS tasks_cache (
  id           TEXT PRIMARY KEY,
  title        TEXT NOT NULL,
  project      TEXT NOT NULL DEFAULT '',
  estimate_min INT NOT NULL DEFAULT 0,
  done         BOOLEAN NOT NULL DEFAULT FALSE,
  top          BOOLEAN NOT NULL DEFAULT FALSE,
  next_step    TEXT NOT NULL DEFAULT '',
  sort         INT NOT NULL DEFAULT 0,
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS events_cache (
  id        TEXT PRIMARY KEY,
  title     TEXT NOT NULL,
  start_at  TIMESTAMPTZ NOT NULL,
  end_at    TIMESTAMPTZ,
  location  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS news_cache (
  id           TEXT PRIMARY KEY,
  source       TEXT NOT NULL DEFAULT '',
  title        TEXT NOT NULL,
  summary      TEXT NOT NULL DEFAULT '',
  published_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS kv (
  key        TEXT PRIMARY KEY,
  value      JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- kv keys used: weather, snapshot, source_status
