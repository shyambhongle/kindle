// Package collector pulls external sources into the cache tables and builds the dashboard snapshot.
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shyam/dashboard/internal/config"
	"github.com/shyam/dashboard/internal/model"
	"github.com/shyam/dashboard/internal/sources"
)

type Collector struct {
	cfg     *config.Config
	pool    *pgxpool.Pool
	Notion  *sources.Notion
	cal     *sources.Calendar
	weather *sources.Weather
	mu      sync.Mutex // one collect/build at a time
}

func New(cfg *config.Config, pool *pgxpool.Pool) *Collector {
	return &Collector{
		cfg:     cfg,
		pool:    pool,
		Notion:  sources.NewNotion(cfg.NotionToken, cfg.NotionTasksDB, cfg.Timezone),
		cal:     &sources.Calendar{URL: cfg.CalendarICSURL, TZ: cfg.Timezone},
		weather: &sources.Weather{Lat: cfg.Latitude, Lon: cfg.Longitude, Location: cfg.WeatherLocation},
	}
}

func (c *Collector) today() time.Time {
	n := time.Now().In(c.cfg.Timezone)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, c.cfg.Timezone)
}

// Collect refreshes every external source (falling back to dummy data), then rebuilds the snapshot.
func (c *Collector) Collect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := map[string]string{}
	started := time.Now()

	// tasks
	if c.Notion.Configured() && !c.cfg.Mock {
		tasks, err := c.Notion.Tasks(ctx)
		if err != nil {
			status["tasks"] = "error: " + err.Error()
			log.Printf("collect: notion: %v", err)
		} else {
			status["tasks"] = "live"
			if err := c.storeTasks(ctx, tasks, true); err != nil {
				return err
			}
		}
	} else {
		status["tasks"] = "mock"
		if err := c.storeTasks(ctx, sources.DummyTasks(), false); err != nil {
			return err
		}
	}

	// calendar
	from, to := c.today().AddDate(0, 0, -40), c.today().AddDate(0, 0, 45)
	if c.cal.Configured() && !c.cfg.Mock {
		events, err := c.cal.Events(ctx, from, to)
		if err != nil {
			status["calendar"] = "error: " + err.Error()
			log.Printf("collect: calendar: %v", err)
		} else {
			status["calendar"] = "live"
			if err := c.storeEvents(ctx, events); err != nil {
				return err
			}
		}
	} else {
		status["calendar"] = "mock"
		if err := c.storeEvents(ctx, sources.DummyEvents(c.cfg.Timezone)); err != nil {
			return err
		}
	}

	// weather
	var w model.Weather
	if !c.cfg.Mock {
		var err error
		if w, err = c.weather.Today(ctx); err != nil {
			status["weather"] = "error: " + err.Error()
			log.Printf("collect: weather: %v", err)
			w = sources.DummyWeather(c.cfg.WeatherLocation)
		} else {
			status["weather"] = "live"
		}
	} else {
		status["weather"] = "mock"
		w = sources.DummyWeather(c.cfg.WeatherLocation)
	}
	if err := c.setKV(ctx, "weather", w); err != nil {
		return err
	}

	// news
	feeds, err := c.feeds(ctx)
	if err != nil {
		return err
	}
	if len(feeds) > 0 && !c.cfg.Mock {
		items, errs := sources.News(ctx, feeds, 12)
		for u, e := range errs {
			log.Printf("collect: feed %s: %v", u, e)
		}
		if len(items) == 0 {
			status["news"] = "error: no feed could be read"
			items = sources.DummyNews()
		} else if len(errs) > 0 {
			status["news"] = fmt.Sprintf("live (%d feeds failed)", len(errs))
		} else {
			status["news"] = "live"
		}
		if err := c.storeNews(ctx, items); err != nil {
			return err
		}
	} else {
		status["news"] = "mock"
		if err := c.storeNews(ctx, sources.DummyNews()); err != nil {
			return err
		}
	}

	if err := c.setKV(ctx, "source_status", status); err != nil {
		return err
	}
	log.Printf("collect: done in %s: %v", time.Since(started).Round(time.Millisecond), status)
	_, err = c.build(ctx)
	return err
}

// Build recomputes the snapshot from the database only (no network). Used after every write.
func (c *Collector) Build(ctx context.Context) (*model.Dashboard, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.build(ctx)
}

// Snapshot returns the stored snapshot, building one if none exists yet.
func (c *Collector) Snapshot(ctx context.Context) ([]byte, error) {
	var raw []byte
	err := c.pool.QueryRow(ctx, `SELECT value FROM kv WHERE key='snapshot'`).Scan(&raw)
	if err == nil {
		return raw, nil
	}
	d, err := c.Build(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(d)
}

/* ---------- cache writers ---------- */

func (c *Collector) storeTasks(ctx context.Context, tasks []model.Task, live bool) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
		// live: source wins on done. mock: keep local done flags so taps persist across restarts.
		q := `INSERT INTO tasks_cache(id,title,project,estimate_min,done,top,next_step,sort,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now())
			ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title, project=EXCLUDED.project, estimate_min=EXCLUDED.estimate_min,
			done=` + map[bool]string{true: "EXCLUDED.done", false: "tasks_cache.done"}[live] + `, top=EXCLUDED.top, next_step=EXCLUDED.next_step, sort=EXCLUDED.sort, updated_at=now()`
		if _, err := tx.Exec(ctx, q, t.ID, t.Title, t.Project, t.EstimateMin, t.Done, t.Top, t.NextStep, t.Sort); err != nil {
			return err
		}
	}
	if live {
		if _, err := tx.Exec(ctx, `DELETE FROM tasks_cache WHERE NOT (id = ANY($1))`, ids); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (c *Collector) storeEvents(ctx context.Context, events []model.Event) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM events_cache`); err != nil {
		return err
	}
	for _, e := range events {
		if _, err := tx.Exec(ctx, `INSERT INTO events_cache(id,title,start_at,end_at,location) VALUES($1,$2,$3,$4,$5) ON CONFLICT (id) DO NOTHING`,
			e.ID, e.Title, e.Start, e.End, e.Location); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (c *Collector) storeNews(ctx context.Context, items []model.News) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM news_cache`); err != nil {
		return err
	}
	for i, n := range items {
		if _, err := tx.Exec(ctx, `INSERT INTO news_cache(id,source,title,summary,published_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT (id) DO NOTHING`,
			n.ID, n.Source, n.Title, n.Summary, time.Now().Add(-time.Duration(i)*time.Second)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (c *Collector) feeds(ctx context.Context) ([]sources.Feed, error) {
	rows, err := c.pool.Query(ctx, `SELECT url, name FROM news_sources ORDER BY url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sources.Feed
	for rows.Next() {
		var f sources.Feed
		if err := rows.Scan(&f.URL, &f.Name); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (c *Collector) setKV(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.pool.Exec(ctx, `INSERT INTO kv(key,value,updated_at) VALUES($1,$2,now()) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, key, raw)
	return err
}

func (c *Collector) getKV(ctx context.Context, key string, v any) error {
	var raw []byte
	if err := c.pool.QueryRow(ctx, `SELECT value FROM kv WHERE key=$1`, key).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

/* ---------- snapshot builder ---------- */

func (c *Collector) build(ctx context.Context) (*model.Dashboard, error) {
	d := &model.Dashboard{GeneratedAt: time.Now().In(c.cfg.Timezone), Sources: map[string]string{}}
	_ = c.getKV(ctx, "weather", &d.Weather)
	_ = c.getKV(ctx, "source_status", &d.Sources)
	var err error
	if d.Events, err = c.events(ctx); err != nil {
		return nil, fmt.Errorf("events: %w", err)
	}
	if d.Tasks, err = c.tasks(ctx); err != nil {
		return nil, fmt.Errorf("tasks: %w", err)
	}
	if d.News, err = c.news(ctx); err != nil {
		return nil, fmt.Errorf("news: %w", err)
	}
	if d.Practice.Guitar, err = c.guitar(ctx); err != nil {
		return nil, fmt.Errorf("guitar: %w", err)
	}
	if d.Practice.German, err = c.german(ctx); err != nil {
		return nil, fmt.Errorf("german: %w", err)
	}
	if d.Practice.Vocab, err = c.vocab(ctx); err != nil {
		return nil, fmt.Errorf("vocab: %w", err)
	}
	if d.Practice.Reading, err = c.reading(ctx); err != nil {
		return nil, fmt.Errorf("reading: %w", err)
	}
	if err := c.setKV(ctx, "snapshot", d); err != nil {
		return nil, err
	}
	return d, nil
}

func (c *Collector) events(ctx context.Context) ([]model.Event, error) {
	rows, err := c.pool.Query(ctx, `SELECT id,title,start_at,end_at,location FROM events_cache ORDER BY start_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		var e model.Event
		var end *time.Time
		if err := rows.Scan(&e.ID, &e.Title, &e.Start, &end, &e.Location); err != nil {
			return nil, err
		}
		e.Start = e.Start.In(c.cfg.Timezone)
		if end != nil {
			e.End = end.In(c.cfg.Timezone)
		} else {
			e.End = e.Start.Add(time.Hour)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (c *Collector) tasks(ctx context.Context) ([]model.Task, error) {
	rows, err := c.pool.Query(ctx, `SELECT id,title,project,estimate_min,done,top,next_step FROM tasks_cache ORDER BY top DESC, done ASC, sort ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Task{}
	for rows.Next() {
		var t model.Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Project, &t.EstimateMin, &t.Done, &t.Top, &t.NextStep); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (c *Collector) news(ctx context.Context) ([]model.News, error) {
	rows, err := c.pool.Query(ctx, `SELECT id,source,title,summary FROM news_cache ORDER BY published_at DESC NULLS LAST LIMIT 12`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.News{}
	for rows.Next() {
		var n model.News
		if err := rows.Scan(&n.ID, &n.Source, &n.Title, &n.Summary); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// last7 returns 7 ints, oldest first, last is today. doneToday is the last element.
func (c *Collector) last7(ctx context.Context, key string) ([]int, bool, error) {
	today := c.today()
	rows, err := c.pool.Query(ctx, `SELECT day FROM practice_log WHERE key=$1 AND done AND day > $2::date - 7 AND day <= $2::date`, key, today)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, false, err
		}
		set[d.Format("2006-01-02")] = true
	}
	out := make([]int, 7)
	for i := 0; i < 7; i++ {
		if set[today.AddDate(0, 0, i-6).Format("2006-01-02")] {
			out[i] = 1
		}
	}
	return out, out[6] == 1, rows.Err()
}

func (c *Collector) guitar(ctx context.Context) (model.Guitar, error) {
	g := model.Guitar{Plan: []string{}}
	var err error
	if g.Last7, g.DoneToday, err = c.last7(ctx, "guitar"); err != nil {
		return g, err
	}
	if err := c.pool.QueryRow(ctx, `SELECT current_week FROM practice_state WHERE key='guitar'`).Scan(&g.Week); err != nil {
		g.Week = 1
	}
	var exercises []byte
	err = c.pool.QueryRow(ctx, `SELECT lesson, exercises, notes FROM guitar_plan WHERE week=$1`, g.Week).Scan(&g.Current, &exercises, &g.Notes)
	if err != nil {
		g.Current = fmt.Sprintf("No plan for week %d", g.Week)
		return g, nil
	}
	_ = json.Unmarshal(exercises, &g.Plan)
	g.Current = fmt.Sprintf("Week %d: %s", g.Week, g.Current)
	if len(g.Plan) > 0 {
		g.Today = g.Plan[0]
		if len(g.Plan) > 1 {
			g.Today += ", then " + g.Plan[1]
		}
	}
	return g, nil
}

// rotate returns n items from a list starting at an index derived from the date, so the
// "of the day" items change daily and the client can still rotate within them per refresh.
func rotateStart(today time.Time, length int) int {
	if length == 0 {
		return 0
	}
	return today.YearDay() % length
}

func (c *Collector) german(ctx context.Context) (model.German, error) {
	g := model.German{Level: "A2", Sentences: []model.DEEN{}, Review: []model.DEEN{}}
	var err error
	if g.Last7, g.DoneToday, err = c.last7(ctx, "german"); err != nil {
		return g, err
	}
	rows, err := c.pool.Query(ctx, `SELECT de,en FROM german_items WHERE kind='sentence' ORDER BY id`)
	if err != nil {
		return g, err
	}
	var all []model.DEEN
	for rows.Next() {
		var s model.DEEN
		if err := rows.Scan(&s.DE, &s.EN); err != nil {
			rows.Close()
			return g, err
		}
		all = append(all, s)
	}
	rows.Close()
	if n := len(all); n > 0 {
		start := rotateStart(c.today(), n)
		for i := 0; i < 4 && i < n; i++ {
			g.Sentences = append(g.Sentences, all[(start+i)%n])
		}
	}
	today := c.today()
	if err := c.pool.QueryRow(ctx, `SELECT count(*) FROM reviews WHERE kind='german' AND due<=$1`, today).Scan(&g.DueReviews); err != nil {
		return g, err
	}
	rows, err = c.pool.Query(ctx, `SELECT i.de, i.en FROM reviews r JOIN german_items i ON i.id=r.item_id WHERE r.kind='german' AND r.due<=$1 ORDER BY r.due, i.id LIMIT 8`, today)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var s model.DEEN
		if err := rows.Scan(&s.DE, &s.EN); err != nil {
			return g, err
		}
		g.Review = append(g.Review, s)
	}
	return g, rows.Err()
}

func (c *Collector) vocab(ctx context.Context) (model.Vocab, error) {
	v := model.Vocab{Words: []model.Word{}, Review: []model.Word{}}
	var err error
	if v.Last7, v.DoneToday, err = c.last7(ctx, "vocab"); err != nil {
		return v, err
	}
	rows, err := c.pool.Query(ctx, `SELECT word,pos,definition,example FROM vocab_items ORDER BY id`)
	if err != nil {
		return v, err
	}
	var all []model.Word
	for rows.Next() {
		var w model.Word
		if err := rows.Scan(&w.Word, &w.Pos, &w.Definition, &w.Example); err != nil {
			rows.Close()
			return v, err
		}
		all = append(all, w)
	}
	rows.Close()
	if n := len(all); n > 0 {
		start := rotateStart(c.today(), n)
		for i := 0; i < 4 && i < n; i++ {
			v.Words = append(v.Words, all[(start+i)%n])
		}
	}
	today := c.today()
	if err := c.pool.QueryRow(ctx, `SELECT count(*) FROM reviews WHERE kind='vocab' AND due<=$1`, today).Scan(&v.DueReviews); err != nil {
		return v, err
	}
	rows, err = c.pool.Query(ctx, `SELECT i.word, i.definition FROM reviews r JOIN vocab_items i ON i.id=r.item_id WHERE r.kind='vocab' AND r.due<=$1 ORDER BY r.due, i.id LIMIT 8`, today)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var w model.Word
		if err := rows.Scan(&w.Word, &w.Definition); err != nil {
			return v, err
		}
		v.Review = append(v.Review, w)
	}
	return v, rows.Err()
}

func (c *Collector) reading(ctx context.Context) (model.Reading, error) {
	r := model.Reading{Sessions: []model.Session{}, Next: []model.Book{}}
	var err error
	if r.Last7, r.DoneToday, err = c.last7(ctx, "reading"); err != nil {
		return r, err
	}
	var bookID int
	var started *time.Time
	err = c.pool.QueryRow(ctx, `SELECT id,title,author,pages,started_on FROM books WHERE status='reading' ORDER BY id LIMIT 1`).
		Scan(&bookID, &r.Book.Title, &r.Book.Author, &r.Book.Pages, &started)
	if err == nil {
		if started != nil {
			r.Book.StartedOn = started.Format("2006-01-02")
		}
		if err := c.pool.QueryRow(ctx, `SELECT coalesce(sum(pages),0) FROM reading_sessions WHERE book_id=$1`, bookID).Scan(&r.Book.Page); err != nil {
			return r, err
		}
		if r.Book.Pages > 0 && r.Book.Page > r.Book.Pages {
			r.Book.Page = r.Book.Pages
		}
		rows, err := c.pool.Query(ctx, `SELECT day,pages,minutes FROM reading_sessions WHERE book_id=$1 ORDER BY day DESC, id DESC LIMIT 6`, bookID)
		if err != nil {
			return r, err
		}
		for rows.Next() {
			var s model.Session
			var d time.Time
			if err := rows.Scan(&d, &s.Pages, &s.Minutes); err != nil {
				rows.Close()
				return r, err
			}
			s.Date = d.Format("2006-01-02")
			r.Sessions = append(r.Sessions, s)
		}
		rows.Close()
	}
	rows, err := c.pool.Query(ctx, `SELECT title,author,pages FROM books WHERE status='next' ORDER BY id LIMIT 4`)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var b model.Book
		if err := rows.Scan(&b.Title, &b.Author, &b.Pages); err != nil {
			return r, err
		}
		r.Next = append(r.Next, b)
	}
	return r, rows.Err()
}

/* ---------- writes from the client ---------- */

func (c *Collector) SetTaskDone(ctx context.Context, id string, done bool) error {
	tag, err := c.pool.Exec(ctx, `UPDATE tasks_cache SET done=$2, updated_at=now() WHERE id=$1`, id, done)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("unknown task")
	}
	if c.Notion.Configured() && !c.cfg.Mock {
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := c.Notion.MarkDone(bg, id, done); err != nil {
				log.Printf("notion: mark done %s: %v", id, err)
			}
		}()
	}
	return nil
}

var practiceKeys = map[string]bool{"guitar": true, "german": true, "vocab": true, "reading": true}

func (c *Collector) SetPracticeDone(ctx context.Context, key string, done bool) error {
	if !practiceKeys[key] {
		return fmt.Errorf("unknown practice key")
	}
	today := c.today()
	if _, err := c.pool.Exec(ctx, `INSERT INTO practice_log(key,day,done) VALUES($1,$2,$3) ON CONFLICT (key,day) DO UPDATE SET done=EXCLUDED.done`, key, today, done); err != nil {
		return err
	}
	// finishing a review session pushes every due item forward (simple spaced repetition: 1,2,4,8... capped at 60 days)
	if done && (key == "german" || key == "vocab") {
		if _, err := c.pool.Exec(ctx, `UPDATE reviews SET reviewed=reviewed+1, interval_days=LEAST(interval_days*2,60), due=$2::date + LEAST(interval_days*2,60) WHERE kind=$1 AND due<=$2`, key, today); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collector) AdvanceGuitarWeek(ctx context.Context, delta int) (int, error) {
	var week int
	err := c.pool.QueryRow(ctx, `UPDATE practice_state SET current_week=GREATEST(1,current_week+$1) WHERE key='guitar' RETURNING current_week`, delta).Scan(&week)
	return week, err
}

func (c *Collector) AddReadingSession(ctx context.Context, pages, minutes int) error {
	if pages < 0 || pages > 2000 || minutes < 0 || minutes > 1440 {
		return fmt.Errorf("pages or minutes out of range")
	}
	var bookID int
	if err := c.pool.QueryRow(ctx, `SELECT id FROM books WHERE status='reading' ORDER BY id LIMIT 1`).Scan(&bookID); err != nil {
		return fmt.Errorf("no book with status 'reading'")
	}
	if _, err := c.pool.Exec(ctx, `INSERT INTO reading_sessions(book_id,day,pages,minutes) VALUES($1,$2,$3,$4)`, bookID, c.today(), pages, minutes); err != nil {
		return err
	}
	return c.SetPracticeDone(ctx, "reading", true)
}

// SeedDummyState fills practice_log and reading_sessions with a believable history (idempotent).
func (c *Collector) SeedDummyState(ctx context.Context) error {
	today := c.today()
	hist := map[string][]int{ // oldest first, 6 days before today (today left for you to tap)
		"guitar":  {1, 1, 0, 1, 1, 1},
		"german":  {1, 1, 1, 1, 0, 1},
		"vocab":   {0, 1, 1, 1, 1, 0},
		"reading": {1, 0, 1, 1, 1, 0},
	}
	for key, days := range hist {
		for i, v := range days {
			if v == 0 {
				continue
			}
			day := today.AddDate(0, 0, i-6)
			if _, err := c.pool.Exec(ctx, `INSERT INTO practice_log(key,day,done) VALUES($1,$2,true) ON CONFLICT DO NOTHING`, key, day); err != nil {
				return err
			}
		}
	}
	var bookID int
	if err := c.pool.QueryRow(ctx, `SELECT id FROM books WHERE status='reading' ORDER BY id LIMIT 1`).Scan(&bookID); err == nil {
		var n int
		_ = c.pool.QueryRow(ctx, `SELECT count(*) FROM reading_sessions WHERE book_id=$1`, bookID).Scan(&n)
		if n == 0 {
			for _, s := range []struct{ off, pages, min int }{{-1, 18, 25}, {-3, 22, 30}, {-4, 12, 15}, {-5, 25, 35}, {-8, 30, 40}, {-10, 36, 45}} {
				if _, err := c.pool.Exec(ctx, `INSERT INTO reading_sessions(book_id,day,pages,minutes) VALUES($1,$2,$3,$4)`, bookID, today.AddDate(0, 0, s.off), s.pages, s.min); err != nil {
					return err
				}
			}
		}
	}
	if _, err := c.pool.Exec(ctx, `UPDATE practice_state SET current_week=12 WHERE key='guitar' AND current_week=1`); err != nil {
		return err
	}
	return nil
}
