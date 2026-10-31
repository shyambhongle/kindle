-- v3: tasks mirror the Notion board; status and previous status let the Kindle undo a "done"
ALTER TABLE tasks_cache ADD COLUMN IF NOT EXISTS status      TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks_cache ADD COLUMN IF NOT EXISTS prev_status TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks_cache ADD COLUMN IF NOT EXISTS kind        TEXT NOT NULL DEFAULT 'backlog';
ALTER TABLE tasks_cache ADD COLUMN IF NOT EXISTS due         DATE;
ALTER TABLE tasks_cache ADD COLUMN IF NOT EXISTS url         TEXT NOT NULL DEFAULT '';
-- kv keys added: tasks_synced (timestamp), notion_schema (json), notion_write_error (text)
