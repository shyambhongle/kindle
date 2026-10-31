// Package collector pulls external sources into the cache tables and builds the dashboard snapshot.
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shyam/dashboard/internal/config"
	"github.com/shyam/dashboard/internal/english"
	"github.com/shyam/dashboard/internal/german"
	"github.com/shyam/dashboard/internal/summaries"
	"github.com/shyam/dashboard/internal/model"
	"github.com/shyam/dashboard/internal/sources"
)

type Collector struct {
	cfg     *config.Config
	pool    *pgxpool.Pool
	Notion  *sources.Notion
	cal     *sources.Calendar
	weather *sources.Weather
	Course  *german.Course   // set by main after construction
	English *english.Service // set by main after construction
	Summaries *summaries.Service // set by main after construction
	mu      sync.Mutex     // one collect/build at a time
}

func New(cfg *config.Config, pool *pgxpool.Pool) *Collector {
	c := &Collector{
		cfg:     cfg,
		pool:    pool,
		Notion: sources.NewNotion(cfg.NotionToken, cfg.NotionTasksDB, cfg.Timezone, sources.StatusMapping{
			Property: cfg.NotionStatus["property"], Top: cfg.NotionStatus["top"], Next: cfg.NotionStatus["next"],
			Backlog: cfg.NotionStatus["backlog"], Hidden: cfg.NotionStatus["hidden"], Done: cfg.NotionStatus["done"],
		}),
		cal:     &sources.Calendar{URL: cfg.CalendarICSURL, TZ: cfg.Timezone},
		weather: sources.NewWeather(cfg.Timezone),
	}
	if cfg.NotionBaseURL != "" {
		c.Notion.BaseURL = cfg.NotionBaseURL
	}
	if cfg.WeatherBaseURL != "" {
		c.weather.BaseURL = cfg.WeatherBaseURL
		c.weather.GeocodeURL = cfg.WeatherBaseURL
	}
	return c
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
	st, err := c.collectTasks(ctx)
	if err != nil {
		return err
	}
	status["tasks"] = st

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

	// weather: every place, 7 days each
	wst, err := c.collectWeather(ctx)
	if err != nil {
		return err
	}
	status["weather"] = wst

	// news (general + security feeds)
	nst, err := c.collectNews(ctx)
	if err != nil {
		return err
	}
	status["news"] = nst

	if err := c.setKV(ctx, "source_status", status); err != nil {
		return err
	}
	ok := true
	for _, v := range status {
		if strings.HasPrefix(v, "error") {
			ok = false
		}
	}
	if raw, err := json.Marshal(status); err == nil {
		_, _ = c.pool.Exec(ctx, `INSERT INTO collect_log(started_at,finished_at,ok,status) VALUES($1,now(),$2,$3)`, started, ok, raw)
		_, _ = c.pool.Exec(ctx, `DELETE FROM collect_log WHERE id < (SELECT max(id) FROM collect_log) - 2000`)
	}
	log.Printf("collect: done in %s: %v", time.Since(started).Round(time.Millisecond), status)
	_, err = c.build(ctx)
	return err
}

// collectTasks pulls the Notion board (or dummy data) into tasks_cache and returns the source status text.
func (c *Collector) collectTasks(ctx context.Context) (string, error) {
	if c.Notion.Configured() && !c.cfg.Mock {
		if schema, err := c.Notion.LoadSchema(ctx); err == nil {
			_ = c.setKV(ctx, "notion_schema", schema)
		}
		tasks, err := c.Notion.Tasks(ctx)
		if err != nil {
			log.Printf("collect: notion: %v", err)
			return "error: " + err.Error(), nil
		}
		if err := c.storeTasks(ctx, tasks, true); err != nil {
			return "", err
		}
		_ = c.setKV(ctx, "tasks_synced", time.Now())
		return "live", nil
	}
	if err := c.storeTasks(ctx, sources.DummyTasks(), false); err != nil {
		return "", err
	}
	_ = c.setKV(ctx, "tasks_synced", time.Now())
	return "mock", nil
}

// Places returns the followed places, home first.
func (c *Collector) Places(ctx context.Context) ([]model.Place, error) {
	rows, err := c.pool.Query(ctx, `SELECT id,name,label,lat,lon,timezone,is_home,sort FROM weather_places ORDER BY is_home DESC, sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Place
	for rows.Next() {
		var p model.Place
		if err := rows.Scan(&p.ID, &p.Name, &p.Label, &p.Lat, &p.Lon, &p.Timezone, &p.Home, &p.Sort); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ensureHomePlace seeds the home place from .env the first time (WEATHER_LAT/LON/LOCATION).
func (c *Collector) ensureHomePlace(ctx context.Context) error {
	var n int
	if err := c.pool.QueryRow(ctx, `SELECT count(*) FROM weather_places`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := c.pool.Exec(ctx, `INSERT INTO weather_places(name,label,lat,lon,timezone,is_home,sort) VALUES($1,$1,$2,$3,$4,true,0)`,
		c.cfg.WeatherLocation, c.cfg.Latitude, c.cfg.Longitude, c.cfg.Timezone.String())
	return err
}

// collectWeather fetches a forecast for every place and stores them; home's today also feeds the Kindle top bar.
func (c *Collector) collectWeather(ctx context.Context) (string, error) {
	if err := c.ensureHomePlace(ctx); err != nil {
		return "", err
	}
	places, err := c.Places(ctx)
	if err != nil {
		return "", err
	}
	var forecasts []model.PlaceForecast
	failed := 0
	for _, p := range places {
		var f model.PlaceForecast
		if c.cfg.Mock {
			f = sources.DummyForecast(p, c.cfg.Timezone)
		} else {
			fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			f, err = c.weather.Forecast(fctx, p)
			cancel()
			if err != nil {
				log.Printf("collect: weather %s: %v", p.Name, err)
				failed++
				f = sources.DummyForecast(p, c.cfg.Timezone)
				f.Error = err.Error()
			}
		}
		forecasts = append(forecasts, f)
	}
	if err := c.setKV(ctx, "forecast", forecasts); err != nil {
		return "", err
	}
	// today at home, for the Kindle top bar
	w := model.Weather{}
	if len(forecasts) > 0 {
		h := forecasts[0]
		w = model.Weather{TempC: h.Current.TempC, Summary: h.Current.Summary, Location: h.Place.Name}
		if len(h.Days) > 0 {
			w.HiC, w.LoC = h.Days[0].HiC, h.Days[0].LoC
		}
	}
	if err := c.setKV(ctx, "weather", w); err != nil {
		return "", err
	}
	switch {
	case c.cfg.Mock:
		return "mock", nil
	case failed == len(places):
		return "error: Open-Meteo unreachable for every place", nil
	case failed > 0:
		return fmt.Sprintf("live (%d of %d places failed)", len(places)-failed, len(places)), nil
	}
	return "live", nil
}

// CollectWeather refreshes only the forecasts (the admin "Refresh" button).
func (c *Collector) CollectWeather(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.collectWeather(ctx)
	if err != nil {
		return st, err
	}
	var status map[string]string
	if c.getKV(ctx, "source_status", &status) != nil || status == nil {
		status = map[string]string{}
	}
	status["weather"] = st
	_ = c.setKV(ctx, "source_status", status)
	_, err = c.build(ctx)
	return st, err
}

// Geocode searches place names through Open-Meteo.
func (c *Collector) Geocode(ctx context.Context, name string) ([]model.Place, error) {
	return c.weather.Geocode(ctx, name)
}

// CollectTasks syncs only the Notion board and rebuilds the snapshot (the admin "Sync now" button).
func (c *Collector) CollectTasks(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.collectTasks(ctx)
	if err != nil {
		return st, err
	}
	var status map[string]string
	if c.getKV(ctx, "source_status", &status) != nil || status == nil {
		status = map[string]string{}
	}
	status["tasks"] = st
	_ = c.setKV(ctx, "source_status", status)
	_, err = c.build(ctx)
	if strings.HasPrefix(st, "error") {
		return st, fmt.Errorf("%s", strings.TrimPrefix(st, "error: "))
	}
	return st, err
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
		var due *string
		if t.Due != "" {
			d := t.Due
			due = &d
		}
		// live: Notion wins on status/done. mock: keep local status so taps persist across restarts.
		q := `INSERT INTO tasks_cache(id,title,project,estimate_min,done,top,next_step,sort,status,kind,due,url,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::date,$12,now())
			ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title, project=EXCLUDED.project, estimate_min=EXCLUDED.estimate_min,
			next_step=EXCLUDED.next_step, sort=EXCLUDED.sort, due=EXCLUDED.due, url=EXCLUDED.url, updated_at=now()`
		if live {
			q += `, done=EXCLUDED.done, top=EXCLUDED.top, status=EXCLUDED.status, kind=EXCLUDED.kind`
		}
		if _, err := tx.Exec(ctx, q, t.ID, t.Title, t.Project, t.EstimateMin, t.Done, t.Top, t.NextStep, t.Sort, t.Status, t.Kind, due, t.URL); err != nil {
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

// collectNews fetches every enabled feed and records per-feed results for the admin page.
func (c *Collector) collectNews(ctx context.Context) (string, error) {
	feeds, err := c.feeds(ctx)
	if err != nil {
		return "", err
	}
	if len(feeds) == 0 || c.cfg.Mock {
		if err := c.storeNews(ctx, sources.DummyNews()); err != nil {
			return "", err
		}
		return "mock", nil
	}
	items, results := sources.News(ctx, feeds, 15)
	items, muted := sources.Curate(items, feeds, c.NewsFilter(ctx), 24, 15)
	_ = c.setKV(ctx, "news_muted", muted)
	failed := 0
	for _, r := range results {
		msg := ""
		if r.Err != nil {
			failed++
			msg = r.Err.Error()
			log.Printf("collect: feed %s: %v", r.URL, r.Err)
		}
		_, _ = c.pool.Exec(ctx, `UPDATE news_sources SET last_error=$2, last_items=$3, last_fetch=now() WHERE url=$1`, r.URL, msg, r.Items)
	}
	if len(items) == 0 {
		if err := c.storeNews(ctx, sources.DummyNews()); err != nil {
			return "", err
		}
		return "error: no feed could be read", nil
	}
	if err := c.storeNews(ctx, items); err != nil {
		return "", err
	}
	if failed > 0 {
		return fmt.Sprintf("live (%d of %d feeds failed)", failed, len(feeds)), nil
	}
	return "live", nil
}

// NewsFilter returns the saved include/exclude keywords, or the defaults.
func (c *Collector) NewsFilter(ctx context.Context) sources.Filter {
	var f sources.Filter
	if err := c.getKV(ctx, "news_filter", &f); err != nil || (len(f.Include) == 0 && len(f.Exclude) == 0) {
		return sources.DefaultFilter()
	}
	return f
}

// SaveNewsFilter stores the keywords; empty lists fall back to the defaults.
func (c *Collector) SaveNewsFilter(ctx context.Context, f sources.Filter) error {
	return c.setKV(ctx, "news_filter", f)
}

// CollectNews refreshes only the feeds (admin "Refresh" button).
func (c *Collector) CollectNews(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.collectNews(ctx)
	if err != nil {
		return st, err
	}
	var status map[string]string
	if c.getKV(ctx, "source_status", &status) != nil || status == nil {
		status = map[string]string{}
	}
	status["news"] = st
	_ = c.setKV(ctx, "source_status", status)
	_, err = c.build(ctx)
	return st, err
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
		at := n.PublishedAt
		if at.IsZero() {
			at = time.Now().Add(-time.Duration(i) * time.Second)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO news_cache(id,source,title,summary,published_at,category,link,alert) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (id) DO NOTHING`,
			n.ID, n.Source, n.Title, n.Summary, at, n.Category, n.Link, n.Alert); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (c *Collector) feeds(ctx context.Context) ([]sources.Feed, error) {
	rows, err := c.pool.Query(ctx, `SELECT url, name, category, filter_mode, max_items FROM news_sources WHERE enabled ORDER BY category, url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sources.Feed
	for rows.Next() {
		var f sources.Feed
		if err := rows.Scan(&f.URL, &f.Name, &f.Category, &f.Mode, &f.MaxItems); err != nil {
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

// GetKV reads a value from the kv table (used by the admin UI).
func (c *Collector) GetKV(ctx context.Context, key string, v any) error { return c.getKV(ctx, key, v) }

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
	d.Forecast = []model.PlaceForecast{}
	_ = c.getKV(ctx, "forecast", &d.Forecast)
	_ = c.getKV(ctx, "source_status", &d.Sources)
	var err error
	if d.Events, err = c.events(ctx); err != nil {
		return nil, fmt.Errorf("events: %w", err)
	}
	if d.Tasks, d.TaskCounts, err = c.tasks(ctx); err != nil {
		return nil, fmt.Errorf("tasks: %w", err)
	}
	_ = c.getKV(ctx, "tasks_synced", &d.TasksSynced)
	if d.News, err = c.news(ctx); err != nil {
		return nil, fmt.Errorf("news: %w", err)
	}
	if d.HN, err = c.NewsByCategory(ctx, "hn", 12); err != nil {
		return nil, fmt.Errorf("hacker news: %w", err)
	}
	if d.Security, err = c.NewsByCategory(ctx, "security", 15); err != nil {
		return nil, fmt.Errorf("security news: %w", err)
	}
	if d.Practice.Guitar, err = c.guitar(ctx); err != nil {
		return nil, fmt.Errorf("guitar: %w", err)
	}
	if d.Practice.German, err = c.german(ctx); err != nil {
		return nil, fmt.Errorf("german: %w", err)
	}
	if c.Course != nil {
		if st, err := c.Course.StatusFor(ctx, c.today()); err == nil {
			if st.Phase != "" {
				d.Practice.German.Level = st.Phase
			}
			if raw, err := json.Marshal(st); err == nil {
				d.Practice.German.Course = raw
			}
		}
	}
	if d.Practice.Vocab, err = c.vocab(ctx); err != nil {
		return nil, fmt.Errorf("vocab: %w", err)
	}
	if d.Practice.Reading, err = c.reading(ctx); err != nil {
		return nil, fmt.Errorf("reading: %w", err)
	}
	if c.English != nil {
		if raw, err := json.Marshal(c.English.StatusFor(ctx, c.today())); err == nil {
			d.Practice.English = raw
		}
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

// AllTasks returns every cached task (for the admin page), in board order.
func (c *Collector) AllTasks(ctx context.Context) ([]model.Task, error) {
	rows, err := c.pool.Query(ctx, `SELECT id,title,project,estimate_min,done,top,next_step,status,kind,due,url FROM tasks_cache ORDER BY sort ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Task{}
	for rows.Next() {
		var t model.Task
		var due *time.Time
		if err := rows.Scan(&t.ID, &t.Title, &t.Project, &t.EstimateMin, &t.Done, &t.Top, &t.NextStep, &t.Status, &t.Kind, &due, &t.URL); err != nil {
			return nil, err
		}
		if due != nil {
			t.Due = due.Format("2006-01-02")
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// tasks returns what the Kindle shows (top, next, done today) plus counts for the whole board.
func (c *Collector) tasks(ctx context.Context) ([]model.Task, model.TaskCounts, error) {
	all, err := c.AllTasks(ctx)
	if err != nil {
		return nil, model.TaskCounts{}, err
	}
	var counts model.TaskCounts
	out := []model.Task{}
	for _, t := range all {
		switch t.Kind {
		case model.KindTop:
			counts.Top++
		case model.KindNext:
			counts.Next++
		case model.KindBacklog:
			counts.Backlog++
		case model.KindDone:
			counts.Done++
		}
		if t.Kind == model.KindTop || t.Kind == model.KindNext || t.Kind == model.KindDone {
			out = append(out, t)
		}
	}
	// top first (open before done), then next, then done
	rank := map[string]int{model.KindTop: 0, model.KindNext: 1, model.KindDone: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Kind] != rank[out[j].Kind] {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		if out[i].Done != out[j].Done {
			return !out[i].Done
		}
		return out[i].Sort < out[j].Sort
	})
	return out, counts, nil
}

// NewsByCategory returns cached items for one category, newest first.
func (c *Collector) NewsByCategory(ctx context.Context, category string, limit int) ([]model.News, error) {
	rows, err := c.pool.Query(ctx, `SELECT id,source,title,summary,link,category,published_at,alert FROM news_cache WHERE category=$1 ORDER BY published_at DESC NULLS LAST LIMIT $2`, category, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.News{}
	for rows.Next() {
		var n model.News
		var at *time.Time
		if err := rows.Scan(&n.ID, &n.Source, &n.Title, &n.Summary, &n.Link, &n.Category, &at, &n.Alert); err != nil {
			return nil, err
		}
		if at != nil {
			n.PublishedAt = at.In(c.cfg.Timezone)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (c *Collector) news(ctx context.Context) ([]model.News, error) {
	return c.NewsByCategory(ctx, "general", 12)
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
	if c.Summaries != nil {
		// book summaries replace page tracking: one book a day from the plan
		st := c.Summaries.StatusFor(ctx, c.today())
		if st.Today != nil {
			r.Book = model.Book{Title: st.Today.Title, Author: st.Today.Author, Page: st.Day, Pages: st.Total, StartedOn: st.StartDate}
		}
		for _, b := range st.Next {
			r.Next = append(r.Next, model.Book{Title: b.Title, Author: b.Author})
		}
		r.Summary, err = json.Marshal(st)
		return r, err
	}
	var bookID int
	var started *time.Time
	var startPage int
	err = c.pool.QueryRow(ctx, `SELECT id,title,author,pages,started_on,start_page FROM books WHERE status='reading' ORDER BY id LIMIT 1`).
		Scan(&bookID, &r.Book.Title, &r.Book.Author, &r.Book.Pages, &started, &startPage)
	if err == nil {
		if started != nil {
			r.Book.StartedOn = started.Format("2006-01-02")
		}
		if err := c.pool.QueryRow(ctx, `SELECT coalesce(sum(pages),0) FROM reading_sessions WHERE book_id=$1`, bookID).Scan(&r.Book.Page); err != nil {
			return r, err
		}
		r.Book.Page += startPage
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

// SetTaskDone marks a task done (status -> Done) or undoes it (status -> the status it had
// before). The local cache changes first so the Kindle redraws at once; the Notion write
// follows synchronously with a short timeout, and a failure is recorded for the admin page.
func (c *Collector) SetTaskDone(ctx context.Context, id string, done bool) error {
	var status, prev string
	if err := c.pool.QueryRow(ctx, `SELECT status, prev_status FROM tasks_cache WHERE id=$1`, id).Scan(&status, &prev); err != nil {
		return fmt.Errorf("unknown task")
	}
	m := c.Notion.Mapping
	var newStatus string
	if done {
		if status == m.Done {
			return nil
		}
		newStatus, prev = m.Done, status
	} else {
		newStatus = prev
		if newStatus == "" || newStatus == m.Done {
			newStatus = m.Top
		}
		prev = ""
	}
	kind := m.Kind(newStatus)
	if _, err := c.pool.Exec(ctx, `UPDATE tasks_cache SET done=$2, top=$3, status=$4, prev_status=$5, kind=$6, updated_at=now() WHERE id=$1`,
		id, kind == model.KindDone, kind == model.KindTop, newStatus, prev, kind); err != nil {
		return err
	}
	if c.Notion.Configured() && !c.cfg.Mock {
		wctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		if err := c.Notion.SetStatus(wctx, id, newStatus); err != nil {
			log.Printf("notion: set status %s -> %q: %v", id, newStatus, err)
			_ = c.setKV(ctx, "notion_write_error", fmt.Sprintf("%s: %s -> %s: %v", time.Now().In(c.cfg.Timezone).Format("Mon 2 Jan 15:04"), id, newStatus, err))
		} else {
			_ = c.setKV(ctx, "notion_write_error", "")
		}
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

// MarkSummaryRead marks a book summary read (or unread). Reading today's book also counts as
// today's reading practice.
func (c *Collector) MarkSummaryRead(ctx context.Context, bookID int, done bool) error {
	if c.Summaries == nil {
		return fmt.Errorf("book summaries are not loaded")
	}
	if err := c.Summaries.MarkRead(ctx, bookID, done); err != nil {
		return err
	}
	b, err := c.Summaries.BookByID(ctx, bookID)
	if err != nil {
		return err
	}
	if done || b.Position == c.Summaries.DayFor(ctx, c.today()) {
		return c.SetPracticeDone(ctx, "reading", done)
	}
	return nil
}

func (c *Collector) AdvanceGuitarWeek(ctx context.Context, delta int) (int, error) {
	if delta > 0 {
		// moving forward marks the week(s) being left as completed today
		_, _ = c.pool.Exec(ctx, `UPDATE guitar_plan SET completed_on=$1 WHERE completed_on IS NULL AND week >= (SELECT current_week FROM practice_state WHERE key='guitar') AND week < (SELECT current_week FROM practice_state WHERE key='guitar') + $2`, c.today(), delta)
	}
	var week int
	err := c.pool.QueryRow(ctx, `UPDATE practice_state SET current_week=GREATEST(1,current_week+$1) WHERE key='guitar' RETURNING current_week`, delta).Scan(&week)
	return week, err
}

// SetGuitarWeek jumps to a specific week without touching completion dates.
func (c *Collector) SetGuitarWeek(ctx context.Context, week int) error {
	if week < 1 || week > 520 {
		return fmt.Errorf("week out of range")
	}
	_, err := c.pool.Exec(ctx, `UPDATE practice_state SET current_week=$1 WHERE key='guitar'`, week)
	return err
}

// Today exposes the collector's notion of "today" (in the configured zone) to other packages.
func (c *Collector) Today() time.Time { return c.today() }

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
