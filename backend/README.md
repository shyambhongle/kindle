# Kindle Dashboard backend (Go + Postgres)

One small binary that collects your data every 15 minutes, keeps it in Postgres, serves the
Kindle API, and hosts a web dashboard at `/admin` where you manage books, the guitar plan,
vocabulary and German, and see history. Works out of the box with dummy data; each source
switches to live data when you set its variable in `.env`.

**Postgres is the source of truth.** The `data/` folder is loaded once, on the first start
when the tables are empty. After that, edit everything in the web dashboard. Daily backups
(`pg_dump`) land in `backups/`.

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
# 1. Postgres role and database (once). It prints a random DATABASE_URL: paste it into .env
psql -d postgres -f deploy/setup-postgres.sql

# 2. Config
cp .env.example .env
go run ./cmd/dashboard token        # paste the output into ACCESS_TOKEN in .env
# then set LISTEN_ADDR to your Mac's LAN IP:  ipconfig getifaddr en0
# and later, ./dashboard url prints the full address for the Kindle

# 3. Build, seed a believable history, run
make build
./dashboard seed                    # imports data/ and adds practice history + reading sessions
./dashboard serve
```

Open `http://<your-ip>:8080/d/<ACCESS_TOKEN>/` on the Kindle (or any browser). With no
Notion token and no calendar URL, tasks, events, and news are dummy; weather is live from
Open-Meteo unless `MOCK=true`.

## Web dashboard

Open `http://localhost:8080/admin` on the Mac (or `http://<mac-ip>:8080/admin` from another
device on your Wi-Fi) and log in with the access token from `.env`.

| Page | What you do there |
|---|---|
| News | Feeds in three categories, general, hacker news and developer security (Socket.dev seeded); add, disable, remove feeds; per-feed errors; security items that read like a compromise are flagged as alerts |
| Weather | Home plus up to four places, each with a 7 day forecast (Open-Meteo, no key); add by city search or coordinates, set home, remove, refresh |
| Tasks | The synced Notion board by status, last sync time and errors, the detected schema and mapping, Sync now, done/undo |
| Overview | Top 3 tasks, this week's practice with "done today" buttons, guitar week, reading progress, next event, source status, Collect now, Backup now |
| German course | The 365 day A1 to B2 plan: day and week, this week's row, tonight's quiz with answers, the Saturday written test with a submission box and the Goethe-style mark, quiz scores, the error log, and the editable 53 week plan |
| English daily | Today's pack from the Claude API: phrases for day-to-day talk, tonight's 2 to 3 minute speaking topic, a 5 minute read-aloud passage with words to notice and delivery tips; done buttons, a note, the archive, and the themes / focus / length that steer generation |
| Books | The daily book summary plan: today's summary in six parts, mark as read, rate it and note what you will apply; the 40+ book plan with dates, add at the end, edit, move up or down, remove unread books, generate or regenerate any summary; length, depth, language and start date |
| Guitar | The week plan: add or edit weeks (lesson, exercises, notes), set the current week, complete a week (moves to the next), see done / current / upcoming |
| English vocab | Add, edit, delete words; paste a CSV to import; see each word's review date and interval; reset a word to today |
| German | Words (spaced repetition) and sentences of the day; add, edit, delete, CSV import |
| History | 12 week practice heatmap, reading sessions, finished books, collector runs with errors |
| Settings | What is loaded from `.env` (secrets masked), source status, setup notes for Google Calendar and Notion, the Kindle URL |

## Running on the Mac permanently

```bash
make build
sh deploy/install-launchd.sh     # starts at login, restarts on crash, logs to dashboard.log
make restart                     # after changing .env or rebuilding
```

Give the Mac a fixed LAN address in your router (DHCP reservation) so the Kindle URL does
not change. The Mac must be awake for the Kindle to refresh; `caffeinate -i` or
System Settings > Battery > "Prevent automatic sleeping on power adapter" helps.

## Commands

| Command | What it does |
|---|---|
| `dashboard token` | Print a new random access token |
| `dashboard url` | Print the Kindle URL for the token now in `.env`. The serve log masks the token, so this is how you read the full address |
| `dashboard migrate` | Create or update tables (also runs on every start) |
| `dashboard import` | Load `data/` into the content tables (safe to repeat) |
| `dashboard seed` | Import, then add dummy practice history. Only for trying it out |
| `dashboard collect` | Pull every source once and rebuild the snapshot |
| `dashboard backup` | Write a compressed `pg_dump` into `BACKUP_DIR` (also runs daily while serving) |
| `dashboard notion-inspect` | Print the Notion board schema and how every task maps to the Kindle |
| `dashboard german-generate [day N / week N]` | Generate German quizzes and Saturday tests (see German course) |
| `dashboard summary-generate [N]` | Generate the book summary for plan day N (default today's book) |
| `dashboard english-generate [YYYY-MM-DD]` | Generate the English daily pack for today or a date (see English daily) |
| `dashboard serve` | Import, collect, then serve HTTP and collect on a timer |

## The data folder (first-run seed only)

| File | Format | Notes |
|---|---|---|
| `data/guitar/plan.yaml` | YAML list | One entry per week: `week`, `lesson`, `exercises[]`, `notes` |
| `data/german/sentences.csv` | `de,en` | Sentence of the day rotates through these daily |
| `data/german/vocab.csv` | `de,en` | Review items with spaced repetition |
| `data/vocab/words.csv` | `word,pos,definition,example` | Word of the day and review items |
| `data/books/summaries.yaml` | YAML | the book summary plan (`start_date`, `words`, style settings, `categories`, ordered `books` with `title`, `author`, `category`, `why`). Loaded once when the plan table is empty |
| `data/news/sources.txt` | `url | name | category` per line (first run only; migration 006 seeds the curated set) | RSS or Atom; category `general`, `hn` or `security` |

These files are loaded only when the content tables are empty, or when you run
`dashboard import` by hand. Day to day, use the web dashboard; it edits Postgres directly.
Running `import` later upserts on a stable key (week number, German text, word, book title)
and never touches your history (done days, reviews, sessions).

## API

All routes live under `/d/{ACCESS_TOKEN}/`. A wrong token returns 404 after a short delay.

| Method and path | Body | Purpose |
|---|---|---|
| `GET /api/dashboard` | | The snapshot JSON (shape in `model/model.go`, mirrored by `koreader-plugin/tools/mock.lua`) |
| `POST /api/collect` | | Force a fresh pull of all sources now |
| `POST /api/tasks/{id}/done` | `{"done":true}` | Mark a task done (status -> Done in Notion) or undo (previous status restored) |
| `POST /api/practice/{key}/done` | `{"done":true}` | `guitar`, `german`, `vocab`, `reading`. For german and vocab, done pushes all due reviews forward |
| `POST /api/practice/guitar/advance` | `{"delta":1}` | Move to the next (or previous) plan week |
| `POST /api/summaries/{id}/read` | `{"done":true}` | Mark a book summary read (counts as today's reading practice) or unread |
| `POST /api/english/{YYYY-MM-DD}/done` | `{"part":"read","done":true}` | Mark the read-aloud (`read`) or the speaking part (`speak`) of a day's English pack done |
| `GET /healthz` | | `ok`, no token needed, for monitoring |

Every write returns the rebuilt dashboard JSON, so a client can redraw immediately.

## Notion setup (tasks)

Notion is the source of truth for tasks. You manage the board in Notion; the backend reads it
every collector cycle and the only thing it ever writes is a task's Status when you mark it
done (or undo) on the Kindle or in the web dashboard.

1. Create an internal integration at notion.so/my-integrations and copy the token.
2. Open your board, "..." > Connections > add the integration.
3. The database id is the 32 hex characters in the board's URL. Put both in `.env`
   (`NOTION_TOKEN`, `NOTION_TASKS_DB`) and restart.
4. Run `./dashboard notion-inspect` to see the schema the backend found and how each task maps.

The backend discovers property names from the schema: the title property (any name), the
status property (`NOTION_STATUS_PROP`, default `Status`), and optionally a date called `Due`,
a select or text called `Project`, a number called `Estimate` (minutes) and a text called
`Next step`. Status options map like this (change in `.env` if your names differ):

| Board status | Kindle | Variable |
|---|---|---|
| Priority | Top 3 (ordered by due date, then board order) | `NOTION_STATUS_TOP` |
| In Progress | listed under the Top 3 | `NOTION_STATUS_NEXT` |
| All Task | counted, not shown | `NOTION_STATUS_BACKLOG` |
| Parked | ignored | `NOTION_STATUS_HIDDEN` |
| Done | done today, if edited today | `NOTION_STATUS_DONE` |

Marking done sets the status to Done; undo restores the status the task had before. If a
write fails, the web dashboard's Tasks page shows the error.

## Weather

Open-Meteo needs no key. The home place is seeded once from `WEATHER_LAT`, `WEATHER_LON` and
`WEATHER_LOCATION` in `.env`; after that, manage places on the Weather page (home plus up to
four more). The snapshot carries `weather` (today at home, for the Kindle top bar) and
`forecast` (every place with current conditions and 7 days: high, low, condition, rain chance,
sunrise and sunset). The Kindle's weather page reads `forecast`.

## News and security alerts

General news is curated for AI, developer tooling, GitHub, tech launches and climate tech.
Migration 006 seeds GitHub Changelog and Blog, Hacker News (150+ points), OpenAI, DeepMind,
Google AI, Hugging Face, Simon Willison, MIT Technology Review, Canary Media, Carbon Brief and
Mongabay. Each feed has a filter mode (`keywords`: an item must match the include list; `all`:
keep everything except excluded topics) and a per-feed cap so no single source floods the list.
The include and exclude keyword lists are edited on the News page; exclude always wins, and the
page shows what was muted on the last run and why, so you can tune the lists in a minute.

Feeds are fetched through a client that refuses to connect to loopback, private and link local
addresses, and that caps the response at 4 MB. A feed URL is the one address in this app that
can be pointed anywhere from the dashboard, so without that a feed, or a redirect from one,
could be used to read whatever else is listening on the Mac. The News page rejects such a URL
when you add it, and the guard runs again on every connection, including redirects.

Feeds have one of three categories, and each one becomes its own list in the snapshot.

* `general` goes to `news`. The Kindle home screen shows the top three in its News column.
* `hn` goes to `hn`. Hacker News was split out by migration 010 so the home screen can give it
  a column of its own beside the general feeds. It is keyword filtered like a general feed.
* `security` goes to `security`. These skip the keyword filter entirely, and each item is
  flagged `alert: true` when the title or summary reads like a real incident (malicious,
  compromised, backdoor, typosquat, CVE, and so on) rather than general advice. A flagged item
  takes over a black banner across the Kindle's news board. Socket.dev's blog
  (`https://socket.dev/api/blog/feed.atom`) is seeded as the first security feed.

Each category gets its own item budget, so a busy Hacker News day cannot crowd out the
curated feeds. Manage all of this on the News page.

## German course (A1 to B2 in 365 days)

The plan from your curriculum lives in `data/german/course.yaml` (53 week rows, test designs
per level, discussion statements) and is loaded into Postgres once; edit weeks on the German
course page afterwards. Day 1 is `start_date` (2 November 2026); week = (day - 1) / 7 + 1.

Every 30 minutes the backend makes sure tonight's quiz exists (Monday to Friday; Friday is
cumulative and repeats three earlier weeks) and this week's Saturday test exists, and after
18:00 it pre-generates tomorrow's quiz. Generation calls the Claude API with the week's row,
the level's question mix from the plan's Test design section, and (at B2 and Prep) your error
log. Quizzes are 14 multiple choice questions so they can be answered by tapping on the Kindle;
the Kindle posts the answers, the backend scores them, marks German practice done, and puts
wrong answers into the error log. The Saturday test has a reading part (multiple choice, graded
on the Kindle or entered by hand) and a writing part you do on paper, type into the dashboard,
and have marked against the Goethe criteria: score, criteria points, biggest errors (added to
the error log), model answer.

| Command | What it does |
|---|---|
| `dashboard german-generate` | Ensure today's quiz and this week's test now |
| `dashboard german-generate day 47` | Generate (or regenerate) the quiz for day 47 |
| `dashboard german-generate week 7` | Generate the Saturday test for week 7 |

Kindle API additions: `POST /api/german/quiz/{day}/result` with `{"answers":[0,2,...]}` and
`POST /api/german/written/{week}/reading` with `{"score":4}`. The snapshot carries
`practice.german.course` with day, week, phase, plan, today's quiz, today's result, this week's
written test and recent scores.

## Book summaries (one book a day)

Instead of reading whole books, you read one detailed summary a day, in a fixed order. The plan
of 40 books (career, communication, psychology, persuasion, discipline, confidence, wisdom,
mental clarity) loads from `data/books/summaries.yaml` the first time the backend starts, in
that file's order, from `start_date` (2026-11-01, so book 40 lands on 10 December). After that
the plan lives in Postgres: add books at the end, edit, move or remove unread ones on the Books
page. Changing the start date moves every book's day.

Each summary is about 5,000 words (around 22 minutes) and is written by the Claude API in two
calls, in the same six-part format every day:

1. Book introduction: the book in one line, what the author teaches, the central argument, who the author is, why it matters for you.
2. Core lessons: the 10 to 15 key concepts, each explained, with an example at work and one from everyday life in India, and a line to remember.
3. Psychological insights: why the ideas work, each labelled "established research", "mixed evidence", "author's framework" or "contested".
4. Practical implementation: 5 to 7 exercises with steps and when to do them.
5. A critical view, plus connections to books earlier in the plan.
6. Final takeaway: the 7 to 10 lessons to remember and a closing paragraph.

The scheduler writes today's summary in the morning and tomorrow's after 18:00. Marking a
summary read (Kindle or dashboard) counts as reading practice for the day. The snapshot keeps
`practice.reading.book` (today's title, `page` = plan day, `pages` = books in the plan) and adds
`practice.reading.summary` with the day, today's book and its full summary, the next three books
and the read count. Summaries are paraphrased, never copied from the books.

## English daily (articulation, speaking, read-aloud)

One pack per calendar day, generated by the Claude API (same key as the German course) and
stored in `english_packs`. A pack has three parts:

- **Phrases** (default 8): expressions a fluent speaker uses at work and with friends, each with
  meaning, example, register (casual / neutral / formal) and the plainer thing you would have said.
- **Speaking topic**: something to talk about alone for 2 to 3 minutes before bed, with three
  angles, useful expressions, a three part structure and a twist question to end on.
- **Read-aloud passage** (default about 700 words, 5 minutes): a story or idea told to be read
  aloud, about personality development, storytelling, psychology, science, history and the other
  themes you list; followed by words worth noticing, reflection questions and delivery tips.

The scheduler generates today's pack in the morning (first run after midnight) and tomorrow's
after 18:00, so the Kindle never waits. A ready pack is never regenerated on its own; press
Regenerate on the dashboard or run `dashboard english-generate 2026-11-05` to replace one.
The last 30 days of titles, topics and phrases go into every prompt so nothing repeats.

Steer it on the English daily page: current focus (free text, goes into every prompt), passage
themes (one per line, rotated day by day), topics to keep out, phrases per day, passage length.

The snapshot carries `practice.english` with `today` (the full pack with `readDone` and
`spokeDone`), `generator` and `days7` (days in the last week with read or speak done). The Kindle
marks a part done with `POST /api/english/{date}/done`.

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
