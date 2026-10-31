-- Run once as a Postgres superuser:  psql -U postgres -f deploy/setup-postgres.sql
-- Change the password before running.
CREATE ROLE dashboard LOGIN PASSWORD 'dashboard';
CREATE DATABASE dashboard OWNER dashboard;
-- The app creates its own tables on first start (embedded migrations).
