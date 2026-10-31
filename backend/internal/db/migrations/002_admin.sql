-- v2: Postgres is the source of truth; the web dashboard edits these tables directly.

ALTER TABLE books ADD COLUMN IF NOT EXISTS start_page  INT NOT NULL DEFAULT 0;   -- pages read before tracking began
ALTER TABLE books ADD COLUMN IF NOT EXISTS finished_on DATE;
ALTER TABLE books ADD COLUMN IF NOT EXISTS notes       TEXT NOT NULL DEFAULT '';
ALTER TABLE books ADD COLUMN IF NOT EXISTS created_at  TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE guitar_plan ADD COLUMN IF NOT EXISTS completed_on DATE;

ALTER TABLE german_items ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE vocab_items  ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE reading_sessions ADD COLUMN IF NOT EXISTS note TEXT NOT NULL DEFAULT '';

-- every collector run, for the history page and for spotting a broken source
CREATE TABLE IF NOT EXISTS collect_log (
  id          SERIAL PRIMARY KEY,
  started_at  TIMESTAMPTZ NOT NULL,
  finished_at TIMESTAMPTZ NOT NULL,
  ok          BOOLEAN NOT NULL,
  status      JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS practice_log_day_idx ON practice_log(day);
CREATE INDEX IF NOT EXISTS reading_sessions_day_idx ON reading_sessions(day);
