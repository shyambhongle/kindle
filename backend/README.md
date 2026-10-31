# Kindle Dashboard backend (Go + Postgres)

One small binary that collects your data every 15 minutes, keeps it in Postgres, and serves
the Kindle client plus a tiny JSON API. Works out of the box with dummy data; each source
switches to live data when you set its variable in `.env`.

```
Notion tasks ─┐
Google ICS ───┤  collector (ticker)        Postgres
Open-Meteo ───┼──────────────────────────► cache tables ──► snapshot (one JSON row)
RSS feeds ────┘                                  ▲                 │
                                                  │                 ▼
data/ folder ──► dashboard import ──► content tables       GET /d/<token>/api/dashboard
                                        ▲                          ▲
Kindle taps ──► POST .../done ──► state tables ──► rebuild ────────┘
```

The Kindle never waits on the internet: Refresh reads the snapshot row. Writes update state
and rebuild the snapshot from the database only.

## Quick start (dummy data, 5 minutes)

```bash
# 1. Postgres role and database (once)
psql -U postgres -f deploy/setup-postgres.sql

# 2. Config
cp .env.example .env
go run ./cmd/dashboard token        # paste the output into ACCESS_TOKEN in .env

# 3. Build, seed a believable history, run
make build
./dashboard seed                    # imports data/ and adds practice history + reading sessions
./dashboard serve
```

Open `http://<your-ip>:8080/d/<ACCESS_TOKEN>/` on the Kindle (or any browser). With no
Notion token and no calendar URL, tasks, events, and news are dummy; weather is live from
Open-Meteo unless `MOCK=true`.

## Commands

| Command | What it does |
|---|---|
| `dashboard token` | Print a new random access token |
| `dashboard migrate` | Create or update tables (also runs on every start) |
| `dashboard import` | Load `data/` into the content tables (safe to repeat) |
| `dashboard seed` | Import, then add dummy practice history. Only for trying it out |
| `dashboard collect` | Pull every source once and rebuild the snapshot |
| `dashboard serve` | Import, collect, then serve HTTP and collect on a timer |

## The data folder (you edit, backend reads)

| File | Format | Notes |
|---|---|---|
| `data/guitar/plan.yaml` | YAML list | One entry per week: `week`, `lesson`, `exercises[]`, `notes` |
| `data/german/sentences.csv` | `de,en` | Sentence of the day rotates through these daily |
| `data/german/vocab.csv` | `de,en` | Review items with spaced repetition |
| `data/vocab/words.csv` | `word,pos,definition,example` | Word of the day and review items |
| `data/books/books.yaml` | YAML list | `title`, `author`, `pages`, `status` (reading/next/done), `started_on` |
| `data/news/sources.txt` | one feed URL per line, optional `| Name` | RSS or Atom |

Import upserts on a stable key (week number, German text, word, book title), so re-importing
after an edit never touches your history. Deleting a line in a file does not delete the row;
remove it in Postgres if you really want it gone.

## API

All routes live under `/d/{ACCESS_TOKEN}/`. A wrong token returns 404 after a short delay.

| Method and path | Body | Purpose |
|---|---|---|
| `GET /` | | The Kindle client, with the API path injected |
| `GET /api/dashboard` | | The snapshot JSON (shape in `model/model.go`, same as the client's mock) |
| `POST /api/collect` | | Force a fresh pull of all sources now |
| `POST /api/tasks/{id}/done` | `{"done":true}` | Mark a task; pushes the checkbox to Notion when configured |
| `POST /api/practice/{key}/done` | `{"done":true}` | `guitar`, `german`, `vocab`, `reading`. For german and vocab, done pushes all due reviews forward |
| `POST /api/practice/guitar/advance` | `{"delta":1}` | Move to the next (or previous) plan week |
| `POST /api/reading/sessions` | `{"pages":20,"minutes":25}` | Log a reading session on the current book |
| `GET /healthz` | | `ok`, no token needed, for monitoring |

Every write returns the rebuilt dashboard JSON, so a client can redraw immediately.

## Notion setup

1. Create an internal integration at notion.so/my-integrations, copy the token.
2. Open your tasks database, "..." > Connections > add the integration.
3. The database id is the 32 hex characters in its URL. Put both in `.env`.
4. Properties the backend reads (rename in `sources/notion.go` if yours differ): `Name` (title),
   `Done` (checkbox), `Top` (checkbox, marks the Top 3), `Project` (select or text),
   `Estimate` (number, minutes), `Next step` (text).

## Google Calendar setup

Google Calendar > Settings > your calendar > Integrate calendar > "Secret address in iCal format".
Put that URL in `CALENDAR_ICS_URL`. Read-only, no OAuth. Google updates the feed with some delay.
Single, all-day, and simple daily or weekly recurring events are supported.

## Security model

* Everything is behind a long random token in the URL, because the Kindle browser cannot send
  headers or store cookies safely. Token compare is constant-time and a miss is slowed down.
* Bind to your LAN IP (`LISTEN_ADDR=192.168.1.20:8080`). Do not forward the port on your router.
* Secrets live only in `.env` (mode 600) and never reach the JSON the Kindle sees.
* Only two kinds of writes exist and they accept tiny, strictly parsed bodies (4 KB max).
* Postgres listens on localhost; the app uses its own role. The Kindle never talks to the database.
* Outbound fetches have timeouts, a 4 MB body cap, and a redirect limit.
* `deploy/dashboard.service` runs it as an unprivileged user with a read-only filesystem view.
* If you later want access away from home, put Tailscale on the box. Keep the Kindle on the LAN.

## Deploying on a Raspberry Pi

```bash
make pi                                # builds dashboard-pi for arm64
scp dashboard-pi pi@pi.local:/opt/dashboard/dashboard
scp -r client data .env pi@pi.local:/opt/dashboard/
# then install deploy/dashboard.service (see comments in the file)
```

Idle footprint is around 15 MB for the binary plus whatever Postgres uses.
