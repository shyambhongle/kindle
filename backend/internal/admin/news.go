package admin

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/shyam/dashboard/internal/model"
	"github.com/shyam/dashboard/internal/sources"
)

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(strings.TrimSuffix(l, "\r"))
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (a *Admin) newsFilterSave(w http.ResponseWriter, r *http.Request) {
	f := sources.Filter{Include: splitLines(r.FormValue("include")), Exclude: splitLines(r.FormValue("exclude"))}
	if r.FormValue("reset") == "1" {
		f = sources.DefaultFilter()
	}
	err := a.col.SaveNewsFilter(r.Context(), f)
	if err == nil {
		_, err = a.col.CollectNews(r.Context())
	}
	back(w, r, "/admin/news", "Filter saved and feeds re-read.", err)
}

type feedRow struct {
	URL       string
	Name      string
	Category  string
	Enabled   bool
	LastError string
	LastItems int
	LastFetch *time.Time
	Mode      string
	MaxItems  int
}

type newsData struct {
	Status   string
	Feeds    []feedRow
	General  []model.News
	HN       []model.News
	Security []model.News
	Alerts   int
	Include  string
	Exclude  string
	Muted    []sources.Muted
}

func (a *Admin) news(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := newsData{}
	var st map[string]string
	if a.col.GetKV(ctx, "source_status", &st) == nil {
		d.Status = st["news"]
	}
	rows, err := a.pool.Query(ctx, `SELECT url,name,category,enabled,last_error,last_items,last_fetch,filter_mode,max_items FROM news_sources ORDER BY category DESC, name, url`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for rows.Next() {
		var f feedRow
		if err := rows.Scan(&f.URL, &f.Name, &f.Category, &f.Enabled, &f.LastError, &f.LastItems, &f.LastFetch, &f.Mode, &f.MaxItems); err != nil {
			rows.Close()
			http.Error(w, err.Error(), 500)
			return
		}
		d.Feeds = append(d.Feeds, f)
	}
	rows.Close()
	flt := a.col.NewsFilter(ctx)
	d.Include, d.Exclude = strings.Join(flt.Include, "\n"), strings.Join(flt.Exclude, "\n")
	_ = a.col.GetKV(ctx, "news_muted", &d.Muted)
	d.General, _ = a.col.NewsByCategory(ctx, "general", 30)
	d.HN, _ = a.col.NewsByCategory(ctx, "hn", 20)
	d.Security, _ = a.col.NewsByCategory(ctx, "security", 25)
	for _, n := range d.Security {
		if n.Alert {
			d.Alerts++
		}
	}
	a.render(w, r, "news", d)
}

// newsCategory keeps an unknown value from reaching the database.
func newsCategory(v string) string {
	switch v {
	case "security", "hn":
		return v
	}
	return "general"
}

func (a *Admin) newsRefresh(w http.ResponseWriter, r *http.Request) {
	_, err := a.col.CollectNews(r.Context())
	back(w, r, "/admin/news", "Feeds refreshed.", err)
}

func (a *Admin) feedCreate(w http.ResponseWriter, r *http.Request) {
	u := strings.TrimSpace(r.FormValue("url"))
	// also refuses anything resolving to the loopback or a private range: feeds are fetched
	// by the server, so an internal address would make it a probe for the rest of the machine
	if err := sources.CheckFeedURL(r.Context(), u); err != nil {
		back(w, r, "/admin/news", "", err)
		return
	}
	cat := newsCategory(r.FormValue("category"))
	_, err := a.pool.Exec(r.Context(), `INSERT INTO news_sources(url,name,category,enabled) VALUES($1,$2,$3,true)
		ON CONFLICT (url) DO UPDATE SET name=EXCLUDED.name, category=EXCLUDED.category, enabled=true`, u, strings.TrimSpace(r.FormValue("name")), cat)
	if err == nil {
		_, err = a.col.CollectNews(r.Context())
	}
	back(w, r, "/admin/news", "Feed added and fetched.", err)
}

func (a *Admin) feedUpdate(w http.ResponseWriter, r *http.Request) {
	u := r.FormValue("url")
	var err error
	switch r.FormValue("action") {
	case "delete":
		_, err = a.pool.Exec(r.Context(), `DELETE FROM news_sources WHERE url=$1`, u)
	case "toggle":
		_, err = a.pool.Exec(r.Context(), `UPDATE news_sources SET enabled = NOT enabled WHERE url=$1`, u)
	case "category":
		_, err = a.pool.Exec(r.Context(), `UPDATE news_sources SET category=$2 WHERE url=$1`, u, newsCategory(r.FormValue("category")))
	case "mode":
		mode := r.FormValue("mode")
		if mode != "all" {
			mode = "keywords"
		}
		_, err = a.pool.Exec(r.Context(), `UPDATE news_sources SET filter_mode=$2 WHERE url=$1`, u, mode)
	case "max":
		n := atoi(r.FormValue("max_items"))
		if n < 1 || n > 20 {
			err = errors.New("max items must be between 1 and 20")
			break
		}
		_, err = a.pool.Exec(r.Context(), `UPDATE news_sources SET max_items=$2 WHERE url=$1`, u, n)
	default:
		err = errors.New("unknown action")
	}
	if err == nil {
		_, err = a.col.CollectNews(r.Context())
	}
	back(w, r, "/admin/news", "Feed updated.", err)
}
