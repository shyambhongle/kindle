# Kindle Dashboard

A personal productivity dashboard for a jailbroken Kindle Paperwhite 3, built around a few
ideas from the psychology of productivity: capture everything, show only a few things at a
time, make the next step concrete, track habits as "n of last 7" instead of streaks, and keep
the tool out of the way.

```
backend/          Go + Postgres backend: collects Notion, Google Calendar (ICS), weather and
                  RSS into one JSON snapshot; serves the clients; stores practice state
koreader-plugin/  KOReader plugin (Lua): native full screen client, landscape, tap to mark done
kindle-client/    Browser client (single HTML file) for the Kindle's built-in browser, plus a
                  KUAL launcher. The first version; kept as a fallback
```

Each folder has its own README with setup steps. Start with `backend/README.md`.

## Quick start

```bash
cd backend
psql -d postgres -f deploy/setup-postgres.sql
cp .env.example .env
go run ./cmd/dashboard token
# paste the token into .env as ACCESS_TOKEN, then:
make build && ./dashboard seed && ./dashboard serve
```

Then install `koreader-plugin/dashboard.koplugin` into `koreader/plugins/` on the Kindle and
set the backend URL from Tools > Dashboard.

## Data you own

Your datasets live in `backend/data/` as plain CSV and YAML (guitar plan, German, vocabulary,
books, news feeds). `dashboard import` loads them into Postgres; your history (done days,
reviews, reading sessions) stays in the database and is never overwritten by an import.

Secrets (`.env`) are ignored by git. Never commit the access token or the Notion token.
