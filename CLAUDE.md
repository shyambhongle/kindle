# Kindle dashboard: project memory

Read this first in every new session. It says what exists, how it is built, what is done and
what comes next. Keep it up to date when you finish a piece of work (see "Updating this file").

## Who and what

Owner: Shyam (Pune, India, timezone Asia/Kolkata). Works at Plant-for-the-Planet.

A personal productivity dashboard shown on a jailbroken Kindle Paperwhite 3 (7th gen,
1072x1448, 300 ppi, landscape) and fed by a Go backend that runs permanently on his MacBook.
Postgres is the only source of truth. A web admin dashboard at `/admin` manages everything.

## How to work with Shyam

- Simple, clear English. No em dashes. Clean design in the shadcn/ui style.
- Ask questions when something is unclear, before building. Offer 2 to 3 options with a recommendation.
- Current focus: the Kindle UI (he asked for it on 1 Nov 2026). Backend and web dashboard are in good shape.
- Never ask him to paste secrets (Notion token, Anthropic key, calendar ICS URL) into chat.
  They live only in `backend/.env`, which is git-ignored.
- He commits and pushes from the Mac himself (GitHub: shyambhongle/kindle, private).
  Do not push for him unless asked.

## Repo layout

```
backend/            Go service (module github.com/shyam/dashboard)
  cmd/dashboard/    main.go: CLI commands and the serve loop
  internal/
    config/         env loading (.env and secrets/.env)
    db/             pgx pool + embedded SQL migrations (migrations/001..009)
    model/          snapshot JSON types sent to the Kindle (model.Dashboard)
    collector/      pulls sources, caches them, builds the single "dashboard" snapshot (kv table)
    sources/        notion.go, calendar.go (ICS), weather.go (Open-Meteo), news.go, filter.go
    llm/            minimal Claude Messages API client returning JSON (240 s timeout, 3 retries)
    german/         German A1 to B2 course: quizzes, Saturday tests, marking, error log
    english/        English daily pack: phrases, bedtime speaking topic, read-aloud passage
    summaries/      Book summary plan: one book a day, ~5,000 word summaries
    admin/          web dashboard: Go templates (templates/*.html), static/admin.css
    server/         HTTP routes for the Kindle under /d/{ACCESS_TOKEN}/
    importer/       first-run seed from data/
    backup/         daily pg_dump to BACKUP_DIR, keeps 30
  data/             seed files only (loaded when tables are empty), never the source of truth
  deploy/           launchd plist + install script (Mac), systemd unit (Pi), setup-postgres.sql
  Makefile          build, run, token, import, seed, collect, backup, install-launchd, restart, test
  README.md         full user docs: pages, commands, API, Notion/calendar/weather/news/course setup
koreader-plugin/    dashboard.koplugin (Lua): installed on the Kindle, Tools > Dashboard
  tools/            off-device layout check: mock snapshot, SVG preview, robustness run
```

## Architecture rules

- Postgres is the source of truth. `data/` is only a first-run seed. Never overwrite edits made in the dashboard.
- Collector pattern: sources are pulled every COLLECT_EVERY (15 min) into cache tables, then one
  `dashboard` snapshot JSON row is built in `kv`. The Kindle only reads the snapshot. Every write
  rebuilds it and returns it.
- Kindle auth: token in the URL path (`/d/{token}/...`), constant-time compare, 300 ms delay on a miss.
  Admin auth: cookie holding an HMAC of the token. Body limit 4 KB for Kindle routes, 1 MB under /admin.
- Admin pages: server-rendered Go templates, plain HTML forms, post-redirect-get with `back()`
  (flash via `?ok=` / `?err=`, keeps `#fragment` last).
- Claude API generators (german, english, summaries) follow one pattern: `Generate` stores a row
  with status ready|error, `EnsureToday` (today, plus tomorrow after 18:00) runs every 30 min from
  the goroutine in main.go `serve`, a CLI command regenerates on demand, and an admin page shows,
  regenerates and edits settings stored in `kv`.
- Practice tiles show "n of last 7 days", never streaks.
- Go 1.22+ ServeMux patterns. Dependencies vendored. Keep it lightweight: pgx, gofeed, yaml.v3 only.

## Features: status

Done and tested (against fake APIs; real APIs need checking on the Mac):

| Area | What it does | Key files |
|---|---|---|
| Tasks | Notion database is the source of truth. Statuses: All Task (backlog), Parked (hidden), In Progress (next), Priority (top), Done. Mark done on Kindle/dashboard writes Done to Notion; undo restores the previous status | sources/notion.go, collector.SetTaskDone, admin/tasks.go |
| Calendar | Google Calendar secret ICS URL, read-only, RRULE daily/weekly | sources/calendar.go |
| Weather | Home + up to 4 places (geocoding search), 7-day forecast each, Open-Meteo | sources/weather.go, admin/weather.go |
| News | Three categories: `general` (curated AI, tech, GitHub, models, climate tech), `hn` (Hacker News, split out by migration 010 so the Kindle can give it its own column) and `security` (Socket.dev, alert flagging, never keyword filtered). Include/exclude keyword filter and muted list | sources/news.go, filter.go, admin/news.go |
| German | 365-day A1 to B2 plan (53 weeks, data/german/course.yaml, start 2026-11-02). Nightly 14-question MCQ quizzes Mon to Fri (graded on Kindle), Saturday written test typed into the dashboard and marked by Claude, error log | german/course.go, admin/course.go |
| English daily | Per day: ~8 phrases, a 2 to 3 min bedtime speaking topic, a ~700 word read-aloud passage. Themes/focus editable. No repeats (last 30 days sent in prompt) | english/english.go, admin/english.go |
| Book summaries | 40-book plan (data/books/summaries.yaml, start 2026-11-01, one book a day, fixed order). ~5,000 word summary in 6 parts (intro, 10 to 15 lessons, psychology with evidence labels, exercises, critical view + connections, takeaways), written in two Claude calls. Read/rating/notes; reading a summary counts as reading practice. Replaced the old page tracker | summaries/summaries.go, admin/books.go |
| Guitar, vocab, German words | 26-week guitar plan, English and German vocab with simple spaced repetition | admin/pages.go |
| Ops | launchd service on the Mac, daily backups, collect log, settings page | deploy/, backup/ |

Kindle side (rebuilt 1 Nov 2026). The KOReader plugin is the only client: the old browser
version and its KUAL launcher were deleted on 1 Nov 2026, along with the code that served it
(`server.client`, `Config.ClientDir`, the `GET /d/{token}/` route). `server.New` no longer
returns an error. Recover from git history if ever needed.

| Area | What it does | Key files |
|---|---|---|
| Home | Rebuilt from Shyam's sketch. Two columns. Left: header (date, weather line that opens the forecast, sync, Refresh, Exit), task board (Top 3 plus a NEXT column plus real `taskCounts`), news board (Hacker News and general side by side, with a black security alert banner when something is flagged). Right: month, four practice buttons carrying today's state, and what is left on today's calendar | dashboard_render.lua |
| Practice buttons | German, Guitar, Reading, English. Each shows one line of today's state ("Quiz 12 of 14", "Day 12: Deep Work") and a done box. The whole button navigates; marking done happens on the page, so a mis-tap cannot mark anything. Vocab lost its tile and is reached from the English page | dashboard_render.lua |
| New pages | Weather (home plus 4 places, 7 days each), English (phrases, speaking topic, read-aloud), Security (flagged items first), rebuilt Reading (today's book, what is inside, up next) | dashboard_render.lua |
| Reader | Paged long text for book summaries and read-aloud passages. Paged by section and then by character budget, since pure Lua cannot measure text. `< Back` / `Next >` / mark read | dashboard_render.lua pageReader |
| Layout tests | Off-device harness: mock snapshot, SVG preview per page, 378 render combinations against empty and partial snapshots | koreader-plugin/tools/ |

Still missing on the Kindle: the German quiz you can tap through, and the Saturday written
test's reading part. The data is in the snapshot under `practice.german.course`.

## Next steps (in rough order, confirm with Shyam first)

1. Install the rebuilt plugin on the Kindle and check the home screen and taps on the real device.
2. German quiz on the Kindle: one question a page, tap an option, POST
   `/api/german/quiz/{day}/result`, then show the score and the explanations. Same machinery as
   the reader. Then the Saturday test's reading part (`/api/german/written/{week}/reading`).
3. Shyam to verify on the Mac with real keys: `./dashboard notion-inspect`, `./dashboard german-generate`,
   `./dashboard english-generate`, `./dashboard summary-generate`, then check each admin page.
4. Commit and push from the Mac: the Mac repo has only the first commit; all later work is uncommitted there.
5. Optional ideas raised earlier: macOS notification for security alerts, secrets in the macOS Keychain.

## Kindle API (all under /d/{ACCESS_TOKEN}/)

`GET /api/dashboard` snapshot. `POST /api/collect`. `POST /api/tasks/{id}/done {"done":bool}`.
`POST /api/practice/{guitar|german|vocab|reading}/done {"done":bool}`. `POST /api/practice/guitar/advance {"delta":1}`.
`POST /api/german/quiz/{day}/result {"answers":[...]}`. `POST /api/german/written/{week}/reading {"score":n}`.
`POST /api/english/{YYYY-MM-DD}/done {"part":"read"|"speak","done":bool}`.
`POST /api/summaries/{id}/read {"done":bool}`. `GET /healthz` (no token).
Snapshot paths: `practice.german.course`, `practice.english`, `practice.reading.summary`,
`forecast`, `taskCounts`, `news`, `hn`, `security`.

## Build, run, test

On the Mac (Go from Homebrew, arm64; Postgres from Homebrew, superuser = macOS user):

```
cd backend
go build -o dashboard ./cmd/dashboard
./dashboard migrate          # also seeds the book plan if empty
./dashboard serve            # http://<LAN-IP>:8080/admin and /d/<token>/
make install-launchd         # run permanently; make restart after rebuilding
```

CLI: `token | url | migrate | import | seed | collect | backup | notion-inspect |
german-generate [day N | week N] | english-generate [YYYY-MM-DD] | summary-generate [N] | serve`.

Env (see `.env.example`): DATABASE_URL, ACCESS_TOKEN (>= 24 chars), LISTEN_ADDR, TZ, COLLECT_EVERY,
WEATHER_*, NOTION_TOKEN, NOTION_TASKS_DB, NOTION_STATUS_*, CALENDAR_ICS_URL, ANTHROPIC_API_KEY,
ANTHROPIC_MODEL (default claude-sonnet-4-5), GERMAN_START_DATE, BACKUP_DIR, DAILY_BACKUP, MOCK.
Test-only overrides: NOTION_BASE_URL, WEATHER_BASE_URL, ANTHROPIC_BASE_URL.

Testing approach used so far: Go unit tests (`go test ./...`, mainly sources/), fake HTTP servers
for Notion, Open-Meteo, feeds and Claude (point the *_BASE_URL vars at them, Claude key `sk-test`),
and Playwright scripts that log in to /admin and click through each page. Add a fake response
shape for every new Claude prompt.

## Gotchas

- Cloud sessions often cannot reach proxy.golang.org, GitHub, Notion, Open-Meteo or feed hosts.
  Build with `GOTOOLCHAIN=local GOFLAGS=-mod=vendor` using vendor/ made on the Mac (`go mod vendor`).
  go.mod says `go 1.25` on the Mac.
- zsh treats inline `#` comments in pasted commands as arguments; give Shyam comment-free commands.
- Adding an admin page: add the name to the `pages` list in admin/admin.go, register routes, add a
  nav link in templates/layout.html.
- New migration = next number in internal/db/migrations; they run on every start.
- Claude-generated text must be paraphrased (book summaries never copy passages).
- `backend/backups/` holds pg_dump files with personal data; keep it out of git.
- Security review done 1 Nov 2026 before the first real commit. Fixed: the access token was
  being logged on every start (launchd sends stdout to dashboard.log); feeds are now fetched
  through `sources.FeedClient`, which refuses to dial loopback, private and link local
  addresses and caps the body at 4 MB; `/admin` sends a Content-Security-Policy;
  `setup-postgres.sql` generates a random password. Still open by choice: the admin session
  cookie is a fixed value derived from the token, so it does not expire server-side and a
  copied cookie works until ACCESS_TOKEN is rotated.
- `sources.AllowPrivateFeedHosts` turns the feed dial guard off. Only tests set it, the same
  idea as the `*_BASE_URL` overrides.
- The serve log masks ACCESS_TOKEN (it would otherwise land in `dashboard.log` under launchd,
  and that token is the only credential for both the Kindle API and /admin). `./dashboard url`
  prints the full address when you need it.
- `dashboard_render.lua` must stay pure ASCII: KOReader is LuaJIT (Lua 5.1), which has no
  `\u{...}` escapes. Symbols go in as decimal byte escapes, e.g. `"\194\176"` for a degree sign.
  `luac -p` from Homebrew lua checks syntax, but it is 5.5, so it will not catch 5.1 problems.
- To check a Kindle layout without the device: `cd koreader-plugin/tools && lua robust.lua`
  (every page against empty and half built snapshots) and `lua preview.lua out` (one SVG per
  page, reports anything outside the canvas). Needs `brew install lua`. See tools/README.md.

## Updating this file

When a feature is finished: move it into the status table, update "Next steps", and note any new
command, route, env var or gotcha. Keep it short; details belong in backend/README.md.
