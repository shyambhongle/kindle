-- Run once as a Postgres superuser:  psql -d postgres -f deploy/setup-postgres.sql
--
-- This generates a random password for the dashboard role and prints the DATABASE_URL to
-- paste into .env. Nothing here is a default you have to remember to change.
--
-- If the role already exists with the old 'dashboard' password, this gives it a new one.

DO $$
DECLARE
  pw text := replace(gen_random_uuid()::text, '-', '') || substr(replace(gen_random_uuid()::text, '-', ''), 1, 8);
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'dashboard') THEN
    EXECUTE format('ALTER ROLE dashboard LOGIN PASSWORD %L', pw);
    RAISE NOTICE 'Existing role "dashboard" given a new password.';
  ELSE
    EXECUTE format('CREATE ROLE dashboard LOGIN PASSWORD %L', pw);
    RAISE NOTICE 'Role "dashboard" created.';
  END IF;
  RAISE NOTICE ' ';
  RAISE NOTICE 'Put this in backend/.env as DATABASE_URL:';
  RAISE NOTICE 'postgres://dashboard:%@localhost:5432/dashboard?sslmode=disable', pw;
  RAISE NOTICE ' ';
END
$$;

-- CREATE DATABASE cannot run inside the block above. If you have run this before, it will say
-- "database dashboard already exists": that is fine, the password above is still applied.
CREATE DATABASE dashboard OWNER dashboard;

-- The app creates its own tables on first start (embedded migrations).
