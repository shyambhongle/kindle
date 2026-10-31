package admin

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shyam/dashboard/internal/backup"
	"github.com/shyam/dashboard/internal/model"
)

func mustSubFS(fsys fs.FS, dir string) fs.FS {
	s, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return s
}

func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

func dateOrNil(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}

func dateOrToday(s string, today time.Time) time.Time {
	if d := dateOrNil(s); d != nil {
		return *d
	}
	return today
}

/* ======================================================= overview */

type overviewData struct {
	Dash      *model.Dashboard
	Snapshot  string
	KindleURL string
	LastRuns  []collectRun
	Week      [7]dayCell
	Backups   []string
	Open      int
	DoneToday int
	Next      *model.Event
	Alerts    int
}

type collectRun struct {
	Started  time.Time
	Finished time.Time
	OK       bool
	Status   map[string]string
}

type dayCell struct {
	Date    time.Time
	Label   string
	Guitar  bool
	German  bool
	Vocab   bool
	Reading bool
	Today   bool
}

func (a *Admin) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := overviewData{KindleURL: fmt.Sprintf("http://<mac-ip>:%s/d/%s/api", portOf(a.cfg.ListenAddr), a.cfg.AccessToken)}
	raw, err := a.col.Snapshot(ctx)
	if err == nil {
		var dash model.Dashboard
		if json.Unmarshal(raw, &dash) == nil {
			d.Dash = &dash
			for _, t := range dash.Tasks {
				if t.Done {
					d.DoneToday++
				} else {
					d.Open++
				}
			}
			for _, n := range dash.Security {
				if n.Alert && time.Since(n.PublishedAt) < 72*time.Hour {
					d.Alerts++
				}
			}
			now := time.Now()
			for i := range dash.Events {
				e := dash.Events[i]
				if !e.End.Before(now) && (d.Next == nil || e.Start.Before(d.Next.Start)) {
					d.Next = &dash.Events[i]
				}
			}
		}
	}
	d.LastRuns, _ = a.collectRuns(ctx, 5)
	d.Week = a.weekCells(ctx)
	d.Backups, _ = backup.List(a.cfg)
	a.render(w, r, "overview", d)
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return "8080"
}

func (a *Admin) collectRuns(ctx context.Context, n int) ([]collectRun, error) {
	rows, err := a.pool.Query(ctx, `SELECT started_at, finished_at, ok, status FROM collect_log ORDER BY id DESC LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []collectRun
	for rows.Next() {
		var c collectRun
		var raw []byte
		if err := rows.Scan(&c.Started, &c.Finished, &c.OK, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &c.Status)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (a *Admin) weekCells(ctx context.Context) [7]dayCell {
	var cells [7]dayCell
	today := a.col.Today()
	done := map[string]map[string]bool{}
	rows, err := a.pool.Query(ctx, `SELECT key, day FROM practice_log WHERE done AND day > $1::date - 7`, today)
	if err == nil {
		for rows.Next() {
			var k string
			var d time.Time
			if rows.Scan(&k, &d) == nil {
				if done[k] == nil {
					done[k] = map[string]bool{}
				}
				done[k][d.Format("2006-01-02")] = true
			}
		}
		rows.Close()
	}
	for i := 0; i < 7; i++ {
		d := today.AddDate(0, 0, i-6)
		k := d.Format("2006-01-02")
		cells[i] = dayCell{Date: d, Label: d.Format("Mon"), Today: i == 6,
			Guitar: done["guitar"][k], German: done["german"][k], Vocab: done["vocab"][k], Reading: done["reading"][k]}
	}
	return cells
}

/* ======================================================= books */

type book struct {
	ID         int
	Title      string
	Author     string
	Pages      int
	Status     string
	StartPage  int
	StartedOn  *time.Time
	FinishedOn *time.Time
	Notes      string
	Read       int // start_page + sessions
	Sessions   []session
}

type session struct {
	ID      int
	Day     time.Time
	Pages   int
	Minutes int
	Note    string
}

/* ======================================================= guitar */

type guitarWeek struct {
	Week        int
	Lesson      string
	Exercises   []string
	Notes       string
	CompletedOn *time.Time
	Current     bool
}

func (a *Admin) guitar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var current int
	_ = a.pool.QueryRow(ctx, `SELECT current_week FROM practice_state WHERE key='guitar'`).Scan(&current)
	rows, err := a.pool.Query(ctx, `SELECT week, lesson, exercises, notes, completed_on FROM guitar_plan ORDER BY week`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var weeks []guitarWeek
	for rows.Next() {
		var g guitarWeek
		var ex []byte
		if err := rows.Scan(&g.Week, &g.Lesson, &ex, &g.Notes, &g.CompletedOn); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.Unmarshal(ex, &g.Exercises)
		g.Current = g.Week == current
		weeks = append(weeks, g)
	}
	var edit *guitarWeek
	if n := atoi(r.URL.Query().Get("edit")); n > 0 {
		for i := range weeks {
			if weeks[i].Week == n {
				edit = &weeks[i]
			}
		}
	}
	next := len(weeks) + 1
	if len(weeks) > 0 {
		next = weeks[len(weeks)-1].Week + 1
	}
	a.render(w, r, "guitar", map[string]any{"Weeks": weeks, "Current": current, "Edit": edit, "NextWeek": next})
}

func (a *Admin) guitarSave(w http.ResponseWriter, r *http.Request) {
	week := atoi(r.FormValue("week"))
	lesson := strings.TrimSpace(r.FormValue("lesson"))
	if week < 1 || lesson == "" {
		back(w, r, "/admin/guitar", "", errors.New("week number and lesson are required"))
		return
	}
	var ex []string
	for _, line := range strings.Split(r.FormValue("exercises"), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			ex = append(ex, l)
		}
	}
	if ex == nil {
		ex = []string{}
	}
	raw, _ := json.Marshal(ex)
	_, err := a.pool.Exec(r.Context(), `INSERT INTO guitar_plan(week,lesson,exercises,notes) VALUES($1,$2,$3,$4)
		ON CONFLICT (week) DO UPDATE SET lesson=EXCLUDED.lesson, exercises=EXCLUDED.exercises, notes=EXCLUDED.notes`,
		week, lesson, string(raw), strings.TrimSpace(r.FormValue("notes")))
	a.rebuild(r)
	back(w, r, "/admin/guitar", fmt.Sprintf("Week %d saved.", week), err)
}

func (a *Admin) guitarCurrent(w http.ResponseWriter, r *http.Request) {
	err := a.col.SetGuitarWeek(r.Context(), atoi(r.FormValue("week")))
	a.rebuild(r)
	back(w, r, "/admin/guitar", "Current week set.", err)
}

func (a *Admin) guitarComplete(w http.ResponseWriter, r *http.Request) {
	week := atoi(r.PathValue("week"))
	var err error
	if r.FormValue("undo") == "1" {
		_, err = a.pool.Exec(r.Context(), `UPDATE guitar_plan SET completed_on=NULL WHERE week=$1`, week)
	} else {
		_, err = a.pool.Exec(r.Context(), `UPDATE guitar_plan SET completed_on=$2 WHERE week=$1`, week, a.col.Today())
		if err == nil {
			// completing the current week moves you to the next one
			var cur int
			if a.pool.QueryRow(r.Context(), `SELECT current_week FROM practice_state WHERE key='guitar'`).Scan(&cur) == nil && cur == week {
				_, err = a.pool.Exec(r.Context(), `UPDATE practice_state SET current_week=$1 WHERE key='guitar'`, week+1)
			}
		}
	}
	a.rebuild(r)
	back(w, r, "/admin/guitar", "Updated.", err)
}

func (a *Admin) guitarDelete(w http.ResponseWriter, r *http.Request) {
	_, err := a.pool.Exec(r.Context(), `DELETE FROM guitar_plan WHERE week=$1`, atoi(r.PathValue("week")))
	a.rebuild(r)
	back(w, r, "/admin/guitar", "Week deleted.", err)
}

/* ======================================================= vocab (English) */

type vocabItem struct {
	ID         int
	Word       string
	Pos        string
	Definition string
	Example    string
	Due        *time.Time
	Interval   int
	Reviewed   int
	IsDue      bool
}

func (a *Admin) vocab(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	today := a.col.Today()
	rows, err := a.pool.Query(ctx, `SELECT v.id, v.word, v.pos, v.definition, v.example, r.due, r.interval_days, r.reviewed
		FROM vocab_items v LEFT JOIN reviews r ON r.kind='vocab' AND r.item_id=v.id ORDER BY r.due NULLS FIRST, v.word`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var items []vocabItem
	due := 0
	for rows.Next() {
		var v vocabItem
		var iv, rv *int
		if err := rows.Scan(&v.ID, &v.Word, &v.Pos, &v.Definition, &v.Example, &v.Due, &iv, &rv); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if iv != nil {
			v.Interval = *iv
		}
		if rv != nil {
			v.Reviewed = *rv
		}
		v.IsDue = v.Due != nil && !v.Due.After(today)
		if v.IsDue {
			due++
		}
		items = append(items, v)
	}
	var edit *vocabItem
	if n := atoi(r.URL.Query().Get("edit")); n > 0 {
		for i := range items {
			if items[i].ID == n {
				edit = &items[i]
			}
		}
	}
	a.render(w, r, "vocab", map[string]any{"Items": items, "Due": due, "Edit": edit, "Today": today})
}

func (a *Admin) vocabSave(w http.ResponseWriter, r *http.Request) {
	word := strings.TrimSpace(r.FormValue("word"))
	def := strings.TrimSpace(r.FormValue("definition"))
	if word == "" || def == "" {
		back(w, r, "/admin/vocab", "", errors.New("word and definition are required"))
		return
	}
	ctx := r.Context()
	var err error
	if id := atoi(r.FormValue("id")); id > 0 {
		_, err = a.pool.Exec(ctx, `UPDATE vocab_items SET word=$2,pos=$3,definition=$4,example=$5 WHERE id=$1`, id, word, strings.TrimSpace(r.FormValue("pos")), def, strings.TrimSpace(r.FormValue("example")))
	} else {
		_, err = a.pool.Exec(ctx, `INSERT INTO vocab_items(word,pos,definition,example) VALUES($1,$2,$3,$4) ON CONFLICT (word) DO UPDATE SET pos=EXCLUDED.pos, definition=EXCLUDED.definition, example=EXCLUDED.example`,
			word, strings.TrimSpace(r.FormValue("pos")), def, strings.TrimSpace(r.FormValue("example")))
		if err == nil {
			_, err = a.pool.Exec(ctx, `INSERT INTO reviews(kind,item_id) SELECT 'vocab', id FROM vocab_items WHERE word=$1 ON CONFLICT DO NOTHING`, word)
		}
	}
	a.rebuild(r)
	back(w, r, "/admin/vocab", "Saved.", err)
}

func (a *Admin) vocabDelete(w http.ResponseWriter, r *http.Request) {
	id := atoi(r.PathValue("id"))
	_, err := a.pool.Exec(r.Context(), `DELETE FROM reviews WHERE kind='vocab' AND item_id=$1`, id)
	if err == nil {
		_, err = a.pool.Exec(r.Context(), `DELETE FROM vocab_items WHERE id=$1`, id)
	}
	a.rebuild(r)
	back(w, r, "/admin/vocab", "Deleted.", err)
}

func (a *Admin) vocabReset(w http.ResponseWriter, r *http.Request) {
	_, err := a.pool.Exec(r.Context(), `UPDATE reviews SET due=$2, interval_days=1 WHERE kind='vocab' AND item_id=$1`, atoi(r.PathValue("id")), a.col.Today())
	a.rebuild(r)
	back(w, r, "/admin/vocab", "Review reset to today.", err)
}

// vocabImport accepts pasted CSV: word,pos,definition,example (header optional)
func (a *Admin) vocabImport(w http.ResponseWriter, r *http.Request) {
	rows, err := parseCSV(r.FormValue("csv"), []string{"word", "pos", "definition", "example"})
	if err != nil {
		back(w, r, "/admin/vocab", "", err)
		return
	}
	n := 0
	for _, row := range rows {
		if row["word"] == "" || row["definition"] == "" {
			continue
		}
		if _, err := a.pool.Exec(r.Context(), `INSERT INTO vocab_items(word,pos,definition,example) VALUES($1,$2,$3,$4) ON CONFLICT (word) DO UPDATE SET pos=EXCLUDED.pos, definition=EXCLUDED.definition, example=EXCLUDED.example`,
			row["word"], row["pos"], row["definition"], row["example"]); err != nil {
			back(w, r, "/admin/vocab", "", err)
			return
		}
		n++
	}
	_, _ = a.pool.Exec(r.Context(), `INSERT INTO reviews(kind,item_id) SELECT 'vocab', id FROM vocab_items ON CONFLICT DO NOTHING`)
	a.rebuild(r)
	back(w, r, "/admin/vocab", fmt.Sprintf("Imported %d words.", n), nil)
}

// parseCSV reads pasted CSV. If the first row matches the expected header it is skipped;
// otherwise columns are taken in the given order.
func parseCSV(text string, cols []string) ([]map[string]string, error) {
	rd := csv.NewReader(strings.NewReader(strings.TrimSpace(text)))
	rd.LazyQuotes = true
	rd.FieldsPerRecord = -1
	rd.TrimLeadingSpace = true
	recs, err := rd.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv: %w", err)
	}
	var out []map[string]string
	for i, rec := range recs {
		if i == 0 && len(rec) > 0 && strings.EqualFold(strings.TrimSpace(rec[0]), cols[0]) {
			continue
		}
		m := map[string]string{}
		for j, c := range cols {
			if j < len(rec) {
				m[c] = strings.TrimSpace(rec[j])
			}
		}
		out = append(out, m)
	}
	return out, nil
}

/* ======================================================= german */

type germanItem struct {
	ID       int
	Kind     string
	DE       string
	EN       string
	Due      *time.Time
	Interval int
	Reviewed int
	IsDue    bool
}

func (a *Admin) german(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	today := a.col.Today()
	rows, err := a.pool.Query(ctx, `SELECT g.id, g.kind, g.de, g.en, r.due, r.interval_days, r.reviewed
		FROM german_items g LEFT JOIN reviews r ON r.kind='german' AND r.item_id=g.id ORDER BY g.kind, r.due NULLS FIRST, g.id`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var words, sentences []germanItem
	due := 0
	for rows.Next() {
		var g germanItem
		var iv, rv *int
		if err := rows.Scan(&g.ID, &g.Kind, &g.DE, &g.EN, &g.Due, &iv, &rv); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if iv != nil {
			g.Interval = *iv
		}
		if rv != nil {
			g.Reviewed = *rv
		}
		g.IsDue = g.Kind == "word" && g.Due != nil && !g.Due.After(today)
		if g.IsDue {
			due++
		}
		if g.Kind == "word" {
			words = append(words, g)
		} else {
			sentences = append(sentences, g)
		}
	}
	var edit *germanItem
	if n := atoi(r.URL.Query().Get("edit")); n > 0 {
		for _, list := range [][]germanItem{words, sentences} {
			for i := range list {
				if list[i].ID == n {
					edit = &list[i]
				}
			}
		}
	}
	a.render(w, r, "german", map[string]any{"Words": words, "Sentences": sentences, "Due": due, "Edit": edit, "Today": today})
}

func (a *Admin) germanSave(w http.ResponseWriter, r *http.Request) {
	kind := r.FormValue("kind")
	de, en := strings.TrimSpace(r.FormValue("de")), strings.TrimSpace(r.FormValue("en"))
	if (kind != "word" && kind != "sentence") || de == "" || en == "" {
		back(w, r, "/admin/german", "", errors.New("kind, German and English are required"))
		return
	}
	ctx := r.Context()
	var err error
	if id := atoi(r.FormValue("id")); id > 0 {
		_, err = a.pool.Exec(ctx, `UPDATE german_items SET kind=$2, de=$3, en=$4 WHERE id=$1`, id, kind, de, en)
	} else {
		_, err = a.pool.Exec(ctx, `INSERT INTO german_items(kind,de,en) VALUES($1,$2,$3) ON CONFLICT (kind,de) DO UPDATE SET en=EXCLUDED.en`, kind, de, en)
	}
	if err == nil && kind == "word" {
		_, err = a.pool.Exec(ctx, `INSERT INTO reviews(kind,item_id) SELECT 'german', id FROM german_items WHERE kind='word' ON CONFLICT DO NOTHING`)
	}
	a.rebuild(r)
	back(w, r, "/admin/german", "Saved.", err)
}

func (a *Admin) germanDelete(w http.ResponseWriter, r *http.Request) {
	id := atoi(r.PathValue("id"))
	_, err := a.pool.Exec(r.Context(), `DELETE FROM reviews WHERE kind='german' AND item_id=$1`, id)
	if err == nil {
		_, err = a.pool.Exec(r.Context(), `DELETE FROM german_items WHERE id=$1`, id)
	}
	a.rebuild(r)
	back(w, r, "/admin/german", "Deleted.", err)
}

func (a *Admin) germanImport(w http.ResponseWriter, r *http.Request) {
	kind := r.FormValue("kind")
	if kind != "word" && kind != "sentence" {
		back(w, r, "/admin/german", "", errors.New("choose words or sentences"))
		return
	}
	rows, err := parseCSV(r.FormValue("csv"), []string{"de", "en"})
	if err != nil {
		back(w, r, "/admin/german", "", err)
		return
	}
	n := 0
	for _, row := range rows {
		if row["de"] == "" || row["en"] == "" {
			continue
		}
		if _, err := a.pool.Exec(r.Context(), `INSERT INTO german_items(kind,de,en) VALUES($1,$2,$3) ON CONFLICT (kind,de) DO UPDATE SET en=EXCLUDED.en`, kind, row["de"], row["en"]); err != nil {
			back(w, r, "/admin/german", "", err)
			return
		}
		n++
	}
	_, _ = a.pool.Exec(r.Context(), `INSERT INTO reviews(kind,item_id) SELECT 'german', id FROM german_items WHERE kind='word' ON CONFLICT DO NOTHING`)
	a.rebuild(r)
	back(w, r, "/admin/german", fmt.Sprintf("Imported %d items.", n), nil)
}

/* ======================================================= history */

type historyData struct {
	Weeks    []historyWeek // oldest first, 12 weeks
	Keys     []string
	Totals   map[string]int
	Sessions []sessionRow
	Runs     []collectRun
	Finished []book
}

type historyWeek struct {
	Start time.Time
	Days  [7]historyDay
}

type historyDay struct {
	Date   time.Time
	Future bool
	Done   map[string]bool
	Count  int
}

type sessionRow struct {
	session
	Book string
}

func (a *Admin) history(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	today := a.col.Today()
	keys := []string{"guitar", "german", "vocab", "reading"}
	// 12 weeks ending this week (Monday start)
	wd := (int(today.Weekday()) + 6) % 7
	thisMonday := today.AddDate(0, 0, -wd)
	first := thisMonday.AddDate(0, 0, -7*11)
	done := map[string]map[string]bool{}
	totals := map[string]int{}
	rows, err := a.pool.Query(ctx, `SELECT key, day FROM practice_log WHERE done AND day >= $1`, first)
	if err == nil {
		for rows.Next() {
			var k string
			var d time.Time
			if rows.Scan(&k, &d) == nil {
				ds := d.Format("2006-01-02")
				if done[ds] == nil {
					done[ds] = map[string]bool{}
				}
				done[ds][k] = true
				totals[k]++
			}
		}
		rows.Close()
	}
	h := historyData{Keys: keys, Totals: totals}
	for wk := 0; wk < 12; wk++ {
		hw := historyWeek{Start: first.AddDate(0, 0, 7*wk)}
		for d := 0; d < 7; d++ {
			day := hw.Start.AddDate(0, 0, d)
			hd := historyDay{Date: day, Future: day.After(today), Done: done[day.Format("2006-01-02")]}
			if hd.Done == nil {
				hd.Done = map[string]bool{}
			}
			hd.Count = len(hd.Done)
			hw.Days[d] = hd
		}
		h.Weeks = append(h.Weeks, hw)
	}
	h.Runs, _ = a.collectRuns(ctx, 30)
	brows, err := a.pool.Query(ctx, `SELECT b.id, b.title, b.author, b.position, s.read_at FROM book_summaries s JOIN book_plan b ON b.id=s.book_id WHERE s.read_at IS NOT NULL ORDER BY s.read_at DESC LIMIT 60`)
	if err == nil {
		for brows.Next() {
			var b book
			if brows.Scan(&b.ID, &b.Title, &b.Author, &b.Pages, &b.FinishedOn) == nil {
				h.Finished = append(h.Finished, b)
			}
		}
		brows.Close()
	}
	a.render(w, r, "history", h)
}

/* ======================================================= settings */

func mask(s string) string {
	if s == "" {
		return "not set"
	}
	if len(s) <= 8 {
		return "set"
	}
	return s[:4] + "…" + s[len(s)-4:]
}

func (a *Admin) settings(w http.ResponseWriter, r *http.Request) {
	var sources map[string]string
	if raw, err := a.col.Snapshot(r.Context()); err == nil {
		var d model.Dashboard
		if json.Unmarshal(raw, &d) == nil {
			sources = d.Sources
		}
	}
	a.render(w, r, "settings", map[string]any{
		"Listen":        a.cfg.ListenAddr,
		"Database":      maskDSN(a.cfg.DatabaseURL),
		"Token":         mask(a.cfg.AccessToken),
		"TZ":            a.cfg.Timezone.String(),
		"CollectEvery":  a.cfg.CollectEvery.String(),
		"Notion":        mask(a.cfg.NotionToken),
		"NotionDB":      mask(a.cfg.NotionTasksDB),
		"ICS":           mask(a.cfg.CalendarICSURL),
		"Weather":       fmt.Sprintf("%s (%.4f, %.4f)", a.cfg.WeatherLocation, a.cfg.Latitude, a.cfg.Longitude),
		"Mock":          a.cfg.Mock,
		"BackupDir":     a.cfg.BackupDir,
		"Sources":       sources,
		"KindleURL":     fmt.Sprintf("http://<mac-ip>:%s/d/%s/api", portOf(a.cfg.ListenAddr), a.cfg.AccessToken),
	})
}

func maskDSN(dsn string) string {
	if i := strings.Index(dsn, "://"); i >= 0 {
		rest := dsn[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 {
			return dsn[:i+3] + "…@" + rest[at+1:]
		}
	}
	return dsn
}
