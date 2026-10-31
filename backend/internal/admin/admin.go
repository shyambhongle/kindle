// Package admin is the web dashboard you open on the Mac to manage books, guitar plan,
// vocabulary, German, and to see history and sync status. Server-rendered Go templates,
// plain HTML forms, one embedded stylesheet. Login = the access token, kept in a cookie.
package admin

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shyam/dashboard/internal/backup"
	"github.com/shyam/dashboard/internal/collector"
	"github.com/shyam/dashboard/internal/config"
	"github.com/shyam/dashboard/internal/summaries"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Admin struct {
	cfg    *config.Config
	pool   *pgxpool.Pool
	col    *collector.Collector
	tmpl   map[string]*template.Template
	cookie string // expected cookie value
}

func New(cfg *config.Config, pool *pgxpool.Pool, col *collector.Collector) (*Admin, error) {
	a := &Admin{cfg: cfg, pool: pool, col: col, tmpl: map[string]*template.Template{}}
	mac := hmac.New(sha256.New, []byte(cfg.AccessToken))
	mac.Write([]byte("admin-session-v1"))
	a.cookie = hex.EncodeToString(mac.Sum(nil))

	funcs := template.FuncMap{
		"date":   func(t time.Time) string { if t.IsZero() { return "" }; return t.Format("2006-01-02") },
		"ptrdate": func(t *time.Time) string { if t == nil { return "" }; return t.Format("2006-01-02") },
		"dt":     func(t time.Time) string { if t.IsZero() { return "" }; return t.In(cfg.Timezone).Format("Mon 2 Jan 15:04") },
		"pct":    func(a, b int) int { if b <= 0 { return 0 }; p := a * 100 / b; if p > 100 { p = 100 }; return p },
		"seq":    func(n int) []int { s := make([]int, n); for i := range s { s[i] = i }; return s },
		"add":    func(a, b int) int { return a + b },
		"join":   strings.Join,
		"round":  func(f float64) int { return int(f + 0.5) },
		"pct100": func(a, b int) int { if b <= 0 { return 0 }; return a * 100 / b },
		"letter": func(i int) string { return string(rune('A' + i)) },
		"nl":     func(s string) template.HTML { return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(s), "\n", "<br>")) },
		"ago": func(t time.Time) string {
			if t.IsZero() { return "" }
			d := time.Since(t)
			switch {
			case d < time.Hour: return fmt.Sprintf("%dm ago", int(d.Minutes()))
			case d < 48*time.Hour: return fmt.Sprintf("%dh ago", int(d.Hours()))
			default: return fmt.Sprintf("%dd ago", int(d.Hours()/24))
			}
		},
		"paras": summaries.Paragraphs,
		"dayname": func(s string) string { t, err := time.Parse("2006-01-02", s); if err != nil { return s }; return t.Format("Mon 2") },
	}
	pages := []string{"overview", "tasks", "weather", "news", "course", "english", "books", "guitar", "vocab", "german", "history", "settings", "login"}
	for _, p := range pages {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/"+p+".html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", p, err)
		}
		a.tmpl[p] = t
	}
	return a, nil
}

// Register mounts the admin routes on mux.
func (a *Admin) Register(mux *http.ServeMux) {
	mux.Handle("GET /admin/static/", http.StripPrefix("/admin/static/", http.FileServerFS(mustSubFS(staticFS, "static"))))
	mux.HandleFunc("GET /admin/login", a.loginPage)
	mux.HandleFunc("POST /admin/login", a.loginPost)
	mux.HandleFunc("POST /admin/logout", a.logout)
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin/", http.StatusFound) })

	p := a.protect
	mux.HandleFunc("GET /admin/{$}", p(a.overview))
	mux.HandleFunc("POST /admin/collect", p(a.collectNow))
	mux.HandleFunc("POST /admin/backup", p(a.backupNow))
	mux.HandleFunc("POST /admin/practice/{key}", p(a.practiceToggle))

	mux.HandleFunc("GET /admin/tasks", p(a.tasks))
	mux.HandleFunc("POST /admin/tasks/sync", p(a.tasksSync))
	mux.HandleFunc("POST /admin/tasks/{id}/done", p(a.taskDone))

	mux.HandleFunc("GET /admin/weather", p(a.weather))
	mux.HandleFunc("POST /admin/weather/refresh", p(a.weatherRefresh))
	mux.HandleFunc("POST /admin/weather/places", p(a.placeCreate))
	mux.HandleFunc("POST /admin/weather/places/{id}/home", p(a.placeHome))
	mux.HandleFunc("POST /admin/weather/places/{id}/delete", p(a.placeDelete))

	mux.HandleFunc("GET /admin/news", p(a.news))
	mux.HandleFunc("POST /admin/news/refresh", p(a.newsRefresh))
	mux.HandleFunc("POST /admin/news/feeds", p(a.feedCreate))
	mux.HandleFunc("POST /admin/news/feeds/update", p(a.feedUpdate))
	mux.HandleFunc("POST /admin/news/filter", p(a.newsFilterSave))

	mux.HandleFunc("GET /admin/course", p(a.course))
	mux.HandleFunc("POST /admin/course/quiz", p(a.courseGenerateQuiz))
	mux.HandleFunc("POST /admin/course/written", p(a.courseGenerateWritten))
	mux.HandleFunc("POST /admin/course/written/{week}/submit", p(a.courseSubmit))
	mux.HandleFunc("POST /admin/course/week", p(a.courseWeekSave))
	mux.HandleFunc("POST /admin/course/errors", p(a.courseErrorAdd))
	mux.HandleFunc("POST /admin/course/errors/{id}/delete", p(a.courseErrorDelete))

	mux.HandleFunc("GET /admin/english", p(a.english))
	mux.HandleFunc("POST /admin/english/generate", p(a.englishGenerate))
	mux.HandleFunc("POST /admin/english/prefs", p(a.englishPrefs))
	mux.HandleFunc("POST /admin/english/{date}/done", p(a.englishDone))
	mux.HandleFunc("POST /admin/english/{date}/note", p(a.englishNote))

	mux.HandleFunc("GET /admin/books", p(a.books))
	mux.HandleFunc("POST /admin/books", p(a.bookCreate))
	mux.HandleFunc("POST /admin/books/settings", p(a.bookSettings))
	mux.HandleFunc("POST /admin/books/{id}", p(a.bookUpdate))
	mux.HandleFunc("POST /admin/books/{id}/delete", p(a.bookDelete))
	mux.HandleFunc("POST /admin/books/{id}/move", p(a.bookMove))
	mux.HandleFunc("POST /admin/books/{id}/generate", p(a.bookGenerate))
	mux.HandleFunc("POST /admin/books/{id}/read", p(a.bookRead))
	mux.HandleFunc("POST /admin/books/{id}/reflect", p(a.bookReflect))

	mux.HandleFunc("GET /admin/guitar", p(a.guitar))
	mux.HandleFunc("POST /admin/guitar", p(a.guitarSave))
	mux.HandleFunc("POST /admin/guitar/current", p(a.guitarCurrent))
	mux.HandleFunc("POST /admin/guitar/{week}/delete", p(a.guitarDelete))
	mux.HandleFunc("POST /admin/guitar/{week}/complete", p(a.guitarComplete))

	mux.HandleFunc("GET /admin/vocab", p(a.vocab))
	mux.HandleFunc("POST /admin/vocab", p(a.vocabSave))
	mux.HandleFunc("POST /admin/vocab/import", p(a.vocabImport))
	mux.HandleFunc("POST /admin/vocab/{id}/delete", p(a.vocabDelete))
	mux.HandleFunc("POST /admin/vocab/{id}/reset", p(a.vocabReset))

	mux.HandleFunc("GET /admin/german", p(a.german))
	mux.HandleFunc("POST /admin/german", p(a.germanSave))
	mux.HandleFunc("POST /admin/german/import", p(a.germanImport))
	mux.HandleFunc("POST /admin/german/{id}/delete", p(a.germanDelete))

	mux.HandleFunc("GET /admin/history", p(a.history))
	mux.HandleFunc("GET /admin/settings", p(a.settings))
}

/* ---------- auth ---------- */

func (a *Admin) authed(r *http.Request) bool {
	c, err := r.Cookie("dashboard_admin")
	if err != nil {
		return false
	}
	return len(c.Value) == len(a.cookie) && subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.cookie)) == 1
}

func (a *Admin) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(r) {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (a *Admin) loginPage(w http.ResponseWriter, r *http.Request) {
	if a.authed(r) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
		return
	}
	a.render(w, r, "login", map[string]any{"Error": r.URL.Query().Get("err")})
}

func (a *Admin) loginPost(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimSpace(r.FormValue("token"))
	if len(tok) != len(a.cfg.AccessToken) || subtle.ConstantTimeCompare([]byte(tok), []byte(a.cfg.AccessToken)) != 1 {
		time.Sleep(500 * time.Millisecond)
		http.Redirect(w, r, "/admin/login?err=1", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "dashboard_admin", Value: a.cookie, Path: "/admin", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 60 * 60 * 24 * 90})
	http.Redirect(w, r, "/admin/", http.StatusFound)
}

func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "dashboard_admin", Value: "", Path: "/admin", HttpOnly: true, MaxAge: -1})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

/* ---------- rendering helpers ---------- */

type page struct {
	Title  string
	Active string
	Flash  string
	Err    string
	Data   any
}

func (a *Admin) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	t := a.tmpl[name]
	var buf bytes.Buffer
	err := t.ExecuteTemplate(&buf, "layout.html", page{
		Title:  strings.ToUpper(name[:1]) + name[1:],
		Active: name,
		Flash:  r.URL.Query().Get("ok"),
		Err:    r.URL.Query().Get("err"),
		Data:   data,
	})
	if err != nil {
		log.Printf("admin: render %s: %v", name, err)
		http.Error(w, "template error: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

// back redirects to a page with a flash message, after a form post (post-redirect-get).
func back(w http.ResponseWriter, r *http.Request, path, ok string, err error) {
	q := url.Values{}
	if err != nil {
		q.Set("err", err.Error())
	} else if ok != "" {
		q.Set("ok", ok)
	}
	frag := ""
	if i := strings.Index(path, "#"); i >= 0 {
		path, frag = path[:i], path[i:]
	}
	if len(q) > 0 {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + q.Encode()
	}
	http.Redirect(w, r, path+frag, http.StatusSeeOther)
}

func (a *Admin) rebuild(r *http.Request) {
	if _, err := a.col.Build(r.Context()); err != nil {
		log.Printf("admin: rebuild: %v", err)
	}
}

/* ---------- overview actions ---------- */

func (a *Admin) collectNow(w http.ResponseWriter, r *http.Request) {
	err := a.col.Collect(r.Context())
	back(w, r, "/admin/", "Collected all sources.", err)
}

func (a *Admin) backupNow(w http.ResponseWriter, r *http.Request) {
	path, err := backup.Run(r.Context(), a.cfg)
	back(w, r, "/admin/", "Backup written to "+path, err)
}

func (a *Admin) practiceToggle(w http.ResponseWriter, r *http.Request) {
	done := r.FormValue("done") == "1"
	err := a.col.SetPracticeDone(r.Context(), r.PathValue("key"), done)
	a.rebuild(r)
	back(w, r, "/admin/", "Updated.", err)
}
