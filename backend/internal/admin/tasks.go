package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/shyam/dashboard/internal/model"
	"github.com/shyam/dashboard/internal/sources"
)

type taskColumn struct {
	Status string
	Kind   string
	Label  string // what the Kindle does with it
	Tasks  []model.Task
}

type tasksData struct {
	Configured bool
	Mock       bool
	Synced     time.Time
	Status     string // source status text
	WriteError string
	Schema     *sources.Schema
	Mapping    sources.StatusMapping
	Columns    []taskColumn
	Total      int
	Unmapped   []string // status options on the board that the mapping does not name
}

func (a *Admin) tasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := tasksData{Configured: a.col.Notion.Configured(), Mock: a.cfg.Mock, Mapping: a.col.Notion.Mapping}
	_ = a.col.GetKV(ctx, "tasks_synced", &d.Synced)
	_ = a.col.GetKV(ctx, "notion_write_error", &d.WriteError)
	var schema sources.Schema
	if a.col.GetKV(ctx, "notion_schema", &schema) == nil && schema.TitleProp != "" {
		d.Schema = &schema
	}
	var st map[string]string
	if a.col.GetKV(ctx, "source_status", &st) == nil {
		d.Status = st["tasks"]
	}
	all, err := a.col.AllTasks(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.Total = len(all)
	m := d.Mapping
	cols := []taskColumn{
		{Status: m.Top, Kind: model.KindTop, Label: "Top 3 on the Kindle"},
		{Status: m.Next, Kind: model.KindNext, Label: "Listed under the Top 3"},
		{Status: m.Backlog, Kind: model.KindBacklog, Label: "Counted only, not on the Kindle"},
		{Status: m.Done, Kind: model.KindDone, Label: "Done today"},
	}
	for _, t := range all {
		for i := range cols {
			if t.Kind == cols[i].Kind {
				cols[i].Tasks = append(cols[i].Tasks, t)
			}
		}
	}
	d.Columns = cols
	if d.Schema != nil {
		known := map[string]bool{m.Top: true, m.Next: true, m.Backlog: true, m.Hidden: true, m.Done: true}
		for _, o := range d.Schema.Options {
			if !known[o] {
				d.Unmapped = append(d.Unmapped, o)
			}
		}
	}
	a.render(w, r, "tasks", d)
}

func (a *Admin) tasksSync(w http.ResponseWriter, r *http.Request) {
	_, err := a.col.CollectTasks(r.Context())
	back(w, r, "/admin/tasks", "Synced with Notion.", err)
}

func (a *Admin) taskDone(w http.ResponseWriter, r *http.Request) {
	done := r.FormValue("done") == "1"
	err := a.col.SetTaskDone(r.Context(), r.PathValue("id"), done)
	a.rebuild(r)
	back(w, r, "/admin/tasks", "Updated in Notion.", err)
}

// keep json imported for GetKV callers in this file
var _ = json.Marshal
