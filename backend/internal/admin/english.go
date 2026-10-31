package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shyam/dashboard/internal/english"
)

type englishData struct {
	Configured bool
	Model      string
	Date       string
	IsToday    bool
	Pack       *english.Pack
	Prefs      english.Prefs
	ThemesText string
	Recent     []english.Summary
	Days7      int
}

func (a *Admin) englishDay(r *http.Request) time.Time {
	if s := r.URL.Query().Get("date"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, a.cfg.Timezone); err == nil {
			return t
		}
	}
	return a.col.English.Today()
}

func (a *Admin) english(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	e := a.col.English
	day := a.englishDay(r)
	d := englishData{Configured: e.Configured(), Model: a.cfg.AnthropicModel, Date: day.Format("2006-01-02"), IsToday: day.Equal(e.Today())}
	d.Pack, _ = e.Get(ctx, day)
	d.Prefs = e.Prefs(ctx)
	d.ThemesText = strings.Join(d.Prefs.Themes, "\n")
	d.Recent, _ = e.Recent(ctx, 30)
	d.Days7 = e.StatusFor(ctx, e.Today()).Days7
	a.render(w, r, "english", d)
}

func (a *Admin) englishGenerate(w http.ResponseWriter, r *http.Request) {
	date := strings.TrimSpace(r.FormValue("date"))
	day, err := time.ParseInLocation("2006-01-02", date, a.cfg.Timezone)
	if err != nil {
		back(w, r, "/admin/english", "", errors.New("date must be YYYY-MM-DD"))
		return
	}
	_, err = a.col.English.Generate(r.Context(), day)
	a.rebuild(r)
	back(w, r, "/admin/english?date="+date, "Pack generated for "+date+".", err)
}

func (a *Admin) englishDone(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	day, err := time.ParseInLocation("2006-01-02", date, a.cfg.Timezone)
	if err != nil {
		back(w, r, "/admin/english", "", errors.New("bad date"))
		return
	}
	err = a.col.English.SetDone(r.Context(), day, r.FormValue("part"), r.FormValue("done") == "1")
	a.rebuild(r)
	back(w, r, "/admin/english?date="+date, "Updated.", err)
}

func (a *Admin) englishNote(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	day, err := time.ParseInLocation("2006-01-02", date, a.cfg.Timezone)
	if err != nil {
		back(w, r, "/admin/english", "", errors.New("bad date"))
		return
	}
	err = a.col.English.SetNote(r.Context(), day, r.FormValue("note"))
	back(w, r, "/admin/english?date="+date, "Note saved.", err)
}

func (a *Admin) englishPrefs(w http.ResponseWriter, r *http.Request) {
	p := english.Prefs{
		Themes: strings.Split(strings.ReplaceAll(r.FormValue("themes"), "\r", ""), "\n"),
		Focus:  strings.TrimSpace(r.FormValue("focus")),
		Avoid:  strings.TrimSpace(r.FormValue("avoid")),
	}
	p.PhraseCount, _ = strconv.Atoi(r.FormValue("phrase_count"))
	p.PassageWords, _ = strconv.Atoi(r.FormValue("passage_words"))
	err := a.col.English.SavePrefs(r.Context(), p)
	back(w, r, "/admin/english#prefs", "Preferences saved. They apply from the next generated pack.", err)
}
