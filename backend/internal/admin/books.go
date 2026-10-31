package admin

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/shyam/dashboard/internal/summaries"
)

type booksData struct {
	Configured bool
	Model      string
	Status     *summaries.Status
	Plan       []summaries.Book
	View       *summaries.Entry // the summary shown (today's by default, or ?id=)
	IsToday    bool
	Edit       *summaries.Book
	Settings   summaries.Settings
	Categories []string
	ReadCount  int
	Ready      int
}

func (a *Admin) books(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s := a.col.Summaries
	d := booksData{Configured: s.Configured(), Model: a.cfg.AnthropicModel}
	d.Status = s.StatusFor(ctx, a.col.Today())
	d.Plan, _ = s.Plan(ctx)
	d.Settings = s.Settings(ctx)
	for c := range d.Settings.Categories {
		d.Categories = append(d.Categories, c)
	}
	sort.Strings(d.Categories)
	for _, b := range d.Plan {
		if b.Read() {
			d.ReadCount++
		}
		if b.Status == "ready" {
			d.Ready++
		}
	}
	if id := atoi(r.URL.Query().Get("id")); id > 0 {
		d.View, _ = s.Get(ctx, id)
	} else if d.Status.Today != nil {
		d.View = d.Status.Today
	}
	d.IsToday = d.View != nil && d.Status.Today != nil && d.View.ID == d.Status.Today.ID
	if id := atoi(r.URL.Query().Get("edit")); id > 0 {
		d.Edit, _ = s.BookByID(ctx, id)
	}
	a.render(w, r, "books", d)
}

func bookFromForm(r *http.Request) summaries.Book {
	return summaries.Book{
		Title:    r.FormValue("title"),
		Author:   r.FormValue("author"),
		Category: r.FormValue("category"),
		Why:      r.FormValue("why"),
	}
}

func (a *Admin) bookCreate(w http.ResponseWriter, r *http.Request) {
	err := a.col.Summaries.AddBook(r.Context(), bookFromForm(r))
	a.rebuild(r)
	back(w, r, "/admin/books#plan", "Book added at the end of the plan.", err)
}

func (a *Admin) bookUpdate(w http.ResponseWriter, r *http.Request) {
	b := bookFromForm(r)
	b.ID = atoi(r.PathValue("id"))
	err := a.col.Summaries.UpdateBook(r.Context(), b)
	a.rebuild(r)
	back(w, r, "/admin/books#plan", "Book saved. Regenerate its summary if you changed the title.", err)
}

func (a *Admin) bookDelete(w http.ResponseWriter, r *http.Request) {
	err := a.col.Summaries.DeleteBook(r.Context(), atoi(r.PathValue("id")))
	a.rebuild(r)
	back(w, r, "/admin/books#plan", "Book removed. Later books moved one day earlier.", err)
}

func (a *Admin) bookMove(w http.ResponseWriter, r *http.Request) {
	delta := -1
	if r.FormValue("dir") == "down" {
		delta = 1
	}
	err := a.col.Summaries.Move(r.Context(), atoi(r.PathValue("id")), delta)
	a.rebuild(r)
	back(w, r, "/admin/books#plan", "Moved.", err)
}

func (a *Admin) bookGenerate(w http.ResponseWriter, r *http.Request) {
	id := atoi(r.PathValue("id"))
	e, err := a.col.Summaries.Generate(r.Context(), id)
	a.rebuild(r)
	msg := ""
	if err == nil {
		msg = fmt.Sprintf("Summary of %s generated: %d words, about %d minutes.", e.Title, e.Summary.WordCount, e.Summary.Minutes)
	}
	back(w, r, fmt.Sprintf("/admin/books?id=%d", id), msg, err)
}

func (a *Admin) bookRead(w http.ResponseWriter, r *http.Request) {
	id := atoi(r.PathValue("id"))
	err := a.col.MarkSummaryRead(r.Context(), id, r.FormValue("done") == "1")
	a.rebuild(r)
	to := r.FormValue("back")
	if !strings.HasPrefix(to, "/admin/") {
		to = fmt.Sprintf("/admin/books?id=%d", id)
	}
	back(w, r, to, "Updated.", err)
}

func (a *Admin) bookReflect(w http.ResponseWriter, r *http.Request) {
	id := atoi(r.PathValue("id"))
	err := a.col.Summaries.Reflect(r.Context(), id, atoi(r.FormValue("rating")), r.FormValue("note"))
	back(w, r, fmt.Sprintf("/admin/books?id=%d#reflect", id), "Saved.", err)
}

func (a *Admin) bookSettings(w http.ResponseWriter, r *http.Request) {
	st := a.col.Summaries.Settings(r.Context())
	st.StartDate = strings.TrimSpace(r.FormValue("start_date"))
	st.Words = atoi(r.FormValue("words"))
	st.Style = strings.TrimSpace(r.FormValue("style"))
	st.Language = strings.TrimSpace(r.FormValue("language"))
	st.Priority = strings.TrimSpace(r.FormValue("priority"))
	st.Reader = strings.TrimSpace(r.FormValue("reader"))
	if st.Style == "" || st.Language == "" {
		back(w, r, "/admin/books#settings", "", errors.New("depth and language are required"))
		return
	}
	err := a.col.Summaries.SaveSettings(r.Context(), st)
	a.rebuild(r)
	back(w, r, "/admin/books#settings", "Settings saved. They apply to summaries generated from now on.", err)
}
