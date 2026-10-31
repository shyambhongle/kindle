package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/shyam/dashboard/internal/german"
)

type errorRow struct {
	ID      int
	Day     int
	Wrong   string
	Correct string
	Rule    string
	Count   int
}

type courseData struct {
	Loaded     bool
	Settings   german.Settings
	Status     *german.Status
	Weeks      []german.Week
	Edit       *german.Week
	Results    []german.Result
	Errors     []errorRow
	ViewDay    int
	ViewQuiz   *german.Quiz
	ViewWeek   int
	ViewWrit   *german.Written
	Configured bool
	Model      string
	StartDate  string
}

func (a *Admin) course(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := a.col.Course
	d := courseData{Configured: c.Configured(), Model: a.cfg.AnthropicModel}
	settings, err := c.Settings(ctx)
	if err != nil {
		a.render(w, r, "course", d)
		return
	}
	d.Loaded, d.Settings, d.StartDate = true, settings, settings.StartDate
	d.Status, _ = c.StatusFor(ctx, a.col.Today())
	d.Weeks, _ = c.Weeks(ctx)
	d.Results, _ = c.RecentResults(ctx, 30)
	if n := atoi(r.URL.Query().Get("edit")); n > 0 {
		if wk, err := c.GetWeek(ctx, n); err == nil {
			d.Edit = wk
		}
	}
	// which quiz / test to show: default today's and this week's
	d.ViewDay = d.Status.Day
	if n := atoi(r.URL.Query().Get("day")); n > 0 {
		d.ViewDay = n
	}
	if d.ViewDay > 0 {
		d.ViewQuiz, _ = c.GetQuiz(ctx, d.ViewDay)
	}
	d.ViewWeek = d.Status.Week
	if n := atoi(r.URL.Query().Get("week")); n > 0 {
		d.ViewWeek = n
	}
	if d.ViewWeek > 0 {
		d.ViewWrit, _ = c.GetWritten(ctx, d.ViewWeek)
	}
	rows, err := a.pool.Query(ctx, `SELECT id, day, wrong, correct, rule, count FROM german_errors ORDER BY count DESC, id DESC LIMIT 40`)
	if err == nil {
		for rows.Next() {
			var e errorRow
			if rows.Scan(&e.ID, &e.Day, &e.Wrong, &e.Correct, &e.Rule, &e.Count) == nil {
				d.Errors = append(d.Errors, e)
			}
		}
		rows.Close()
	}
	a.render(w, r, "course", d)
}

func (a *Admin) courseGenerateQuiz(w http.ResponseWriter, r *http.Request) {
	day := atoi(r.FormValue("day"))
	if day < 1 || day > 371 {
		back(w, r, "/admin/course", "", errors.New("day must be between 1 and 371"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()
	_, err := a.col.Course.GenerateQuiz(ctx, day)
	a.rebuild(r)
	back(w, r, "/admin/course?day="+itoa(day), "Quiz generated.", err)
}

func (a *Admin) courseGenerateWritten(w http.ResponseWriter, r *http.Request) {
	week := atoi(r.FormValue("week"))
	if week < 1 || week > 53 {
		back(w, r, "/admin/course", "", errors.New("week must be between 1 and 53"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()
	_, err := a.col.Course.GenerateWritten(ctx, week)
	a.rebuild(r)
	back(w, r, "/admin/course?week="+itoa(week), "Written test generated.", err)
}

func (a *Admin) courseSubmit(w http.ResponseWriter, r *http.Request) {
	week := atoi(r.PathValue("week"))
	var reading *int
	if v := strings.TrimSpace(r.FormValue("reading_score")); v != "" {
		n := atoi(v)
		reading = &n
	}
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()
	_, err := a.col.Course.Submit(ctx, week, r.FormValue("submission"), reading)
	if err == nil {
		_ = a.col.SetPracticeDone(ctx, "german", true)
	}
	a.rebuild(r)
	back(w, r, "/admin/course?week="+itoa(week), "Marked. Scroll down for the result.", err)
}

func (a *Admin) courseWeekSave(w http.ResponseWriter, r *http.Request) {
	wk := german.Week{
		Week: atoi(r.FormValue("week")), Phase: strings.TrimSpace(r.FormValue("phase")),
		Grammar: strings.TrimSpace(r.FormValue("grammar")), Vocab: strings.TrimSpace(r.FormValue("vocab")),
		Phrases: strings.TrimSpace(r.FormValue("phrases")), Speaking: strings.TrimSpace(r.FormValue("speaking")),
		Saturday: strings.TrimSpace(r.FormValue("saturday")), Checkpoint: strings.TrimSpace(r.FormValue("checkpoint")),
	}
	if wk.Week < 1 || wk.Phase == "" || wk.Grammar == "" {
		back(w, r, "/admin/course", "", errors.New("week, phase and grammar are required"))
		return
	}
	err := a.col.Course.SaveWeek(r.Context(), wk)
	a.rebuild(r)
	back(w, r, "/admin/course", "Week saved.", err)
}

func (a *Admin) courseErrorAdd(w http.ResponseWriter, r *http.Request) {
	err := a.col.Course.AddError(r.Context(), 0, german.ErrRow{Wrong: r.FormValue("wrong"), Correct: r.FormValue("correct"), Rule: r.FormValue("rule")})
	back(w, r, "/admin/course", "Added to the error log.", err)
}

func (a *Admin) courseErrorDelete(w http.ResponseWriter, r *http.Request) {
	err := a.col.Course.DeleteError(r.Context(), atoi(r.PathValue("id")))
	back(w, r, "/admin/course", "Removed.", err)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
