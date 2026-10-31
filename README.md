# Kindle Dashboard

A personal productivity dashboard for a jailbroken Kindle Paperwhite 3, built around a few
ideas from the psychology of productivity: capture everything, show only a few things at a
time, make the next step concrete, track habits as "n of last 7" instead of streaks, and keep
the tool out of the way.

```
backend/          Go + Postgres backend: collects Notion, Google Calendar (ICS), weather and
                  RSS into one JSON snapshot; serves the clients; stores practice state
koreader-plugin/  KOReader plugin (Lua): the Kindle client. Native full screen, landscape,
                  tap to mark done. tools/ checks the layout off-device
```

Each folder has its own README with setup steps. Start with `backend/README.md`.

## Quick start

```bash
cd backend
psql -d postgres -f deploy/setup-postgres.sql
cp .env.example .env
go run ./cmd/dashboard token
make build && ./dashboard seed && ./dashboard serve
```

Two values go into `.env` as you go: the `DATABASE_URL` that `setup-postgres.sql` prints, and
the token that `dashboard token` prints, as `ACCESS_TOKEN`. Also set `LISTEN_ADDR` to your LAN
IP (`ipconfig getifaddr en0`).

Then install `koreader-plugin/dashboard.koplugin` into `koreader/plugins/` on the Kindle and
set the backend URL from Tools > Dashboard.

## Data you own

Your datasets live in `backend/data/` as plain CSV and YAML (guitar plan, German, vocabulary,
books, news feeds). `dashboard import` loads them into Postgres; your history (done days,
reviews, reading sessions) stays in the database and is never overwritten by an import.

Secrets (`.env`) are ignored by git. Never commit the access token or the Notion token.
`deploy/setup-postgres.sql` generates a random database password rather than shipping a default,
the serve log masks the access token, and `./dashboard url` prints the full address when you
need it. Set `LISTEN_ADDR` to your LAN IP so the dashboard is not offered to the whole network.
