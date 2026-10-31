package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/shyam/dashboard/internal/model"
)

// Notion reads tasks from a Notion database (your board) using an internal integration token.
// Notion is the source of truth; the only write is changing a task's Status when you tap
// done or undo on the Kindle.
//
// The database schema is read at runtime, so property names are discovered, not hard-coded:
//   - the title property (whatever it is called) becomes the task title
//   - the Status property (type "status", name configurable) decides where a task shows up
//   - optional properties are used when present: Due (date), Project (select or text),
//     Estimate (number, minutes), Next step (rich text)
type Notion struct {
	Token   string
	DBID    string
	TZ      *time.Location
	Mapping StatusMapping
	BaseURL string // overridable for tests

	schema *Schema
}

// StatusMapping names the status options on your board and what they mean for the Kindle.
type StatusMapping struct {
	Property string // name of the status property, default "Status"
	Top      string // shown as Top 3            default "Priority"
	Next     string // shown under the Top 3     default "In Progress"
	Backlog  string // counted, not shown        default "All Task"
	Hidden   string // ignored                   default "Parked"
	Done     string // done                      default "Done"
}

func DefaultMapping() StatusMapping {
	return StatusMapping{Property: "Status", Top: "Priority", Next: "In Progress", Backlog: "All Task", Hidden: "Parked", Done: "Done"}
}

// Kind returns the task kind for a status option name.
func (m StatusMapping) Kind(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case strings.ToLower(m.Top):
		return model.KindTop
	case strings.ToLower(m.Next):
		return model.KindNext
	case strings.ToLower(m.Done):
		return model.KindDone
	case strings.ToLower(m.Hidden):
		return model.KindHidden
	case strings.ToLower(m.Backlog):
		return model.KindBacklog
	}
	return model.KindBacklog // unknown statuses are treated as backlog so nothing is lost silently
}

// Schema is the subset of the database definition we care about.
type Schema struct {
	Title      string              // database title
	TitleProp  string              // name of the title property
	StatusProp string              // name of the status property actually found
	Options    []string            // status options in Notion's order
	Props      map[string]string   // property name -> type
	Due        string              // detected optional property names ("" if absent)
	Project    string
	Estimate   string
	NextStep   string
}

func NewNotion(token, dbID string, tz *time.Location, m StatusMapping) *Notion {
	if m.Property == "" {
		m = DefaultMapping()
	}
	return &Notion{Token: token, DBID: dbID, TZ: tz, Mapping: m, BaseURL: "https://api.notion.com"}
}

func (n *Notion) Configured() bool { return n.Token != "" && n.DBID != "" }

func (n *Notion) headers(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+n.Token)
	req.Header.Set("Notion-Version", "2022-06-28")
	req.Header.Set("Content-Type", "application/json")
}

func (n *Notion) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, n.BaseURL+path, rd)
	if err != nil {
		return err
	}
	n.headers(req)
	resp, err := Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Message == "" {
			e.Message = string(b)
		}
		return fmt.Errorf("notion %d: %s", resp.StatusCode, strings.TrimSpace(e.Message))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out)
}

// LoadSchema reads the database definition. It is cached for the process lifetime after the first success.
func (n *Notion) LoadSchema(ctx context.Context) (*Schema, error) {
	if n.schema != nil {
		return n.schema, nil
	}
	var db struct {
		Title []struct {
			PlainText string `json:"plain_text"`
		} `json:"title"`
		Properties map[string]struct {
			Type   string `json:"type"`
			Status struct {
				Options []struct {
					Name string `json:"name"`
				} `json:"options"`
			} `json:"status"`
			Select struct {
				Options []struct {
					Name string `json:"name"`
				} `json:"options"`
			} `json:"select"`
		} `json:"properties"`
	}
	if err := n.do(ctx, "GET", "/v1/databases/"+n.DBID, nil, &db); err != nil {
		return nil, err
	}
	s := &Schema{Props: map[string]string{}}
	for _, t := range db.Title {
		s.Title += t.PlainText
	}
	for name, p := range db.Properties {
		s.Props[name] = p.Type
		lname := strings.ToLower(name)
		switch {
		case p.Type == "title":
			s.TitleProp = name
		case (p.Type == "status" || p.Type == "select") && strings.EqualFold(name, n.Mapping.Property):
			s.StatusProp = name
			opts := p.Status.Options
			if p.Type == "select" {
				opts = p.Select.Options
			}
			for _, o := range opts {
				s.Options = append(s.Options, o.Name)
			}
		case p.Type == "date" && (lname == "due" || lname == "due date" || lname == "date" || lname == "deadline"):
			s.Due = name
		case (p.Type == "select" || p.Type == "rich_text" || p.Type == "multi_select") && (lname == "project" || lname == "area" || lname == "category"):
			s.Project = name
		case p.Type == "number" && (lname == "estimate" || lname == "minutes" || lname == "estimate (min)"):
			s.Estimate = name
		case p.Type == "rich_text" && (lname == "next step" || lname == "next" || lname == "next action"):
			s.NextStep = name
		}
	}
	if s.StatusProp == "" {
		// fall back to the first status-typed property, whatever its name
		for name, t := range s.Props {
			if t == "status" {
				s.StatusProp = name
				break
			}
		}
	}
	if s.TitleProp == "" {
		return nil, fmt.Errorf("database has no title property")
	}
	if s.StatusProp == "" {
		return nil, fmt.Errorf("no status property named %q found (properties: %s)", n.Mapping.Property, strings.Join(propNames(s.Props), ", "))
	}
	n.schema = s
	return s, nil
}

func propNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, t := range m {
		out = append(out, k+" ("+t+")")
	}
	sort.Strings(out)
	return out
}

type notionPage struct {
	ID         string                     `json:"id"`
	URL        string                     `json:"url"`
	Created    time.Time                  `json:"created_time"`
	LastEdited time.Time                  `json:"last_edited_time"`
	Properties map[string]json.RawMessage `json:"properties"`
}

// Tasks returns every task on the board except hidden ones. Done tasks are kept only if
// they were edited today (so "done today" works without a status-change timestamp).
func (n *Notion) Tasks(ctx context.Context) ([]model.Task, error) {
	s, err := n.LoadSchema(ctx)
	if err != nil {
		return nil, err
	}
	statusType := s.Props[s.StatusProp]
	body := map[string]any{
		"page_size": 100,
		"filter": map[string]any{
			"property":  s.StatusProp,
			statusType:  map[string]any{"does_not_equal": n.Mapping.Hidden},
		},
	}
	var out []model.Task
	var cursor string
	for page := 0; page < 10; page++ { // hard cap: 1000 tasks
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		var res struct {
			Results    []notionPage `json:"results"`
			HasMore    bool         `json:"has_more"`
			NextCursor string       `json:"next_cursor"`
		}
		if err := n.do(ctx, "POST", "/v1/databases/"+n.DBID+"/query", body, &res); err != nil {
			return nil, err
		}
		for _, p := range res.Results {
			t := n.toTask(s, p)
			if t.Kind == model.KindHidden {
				continue
			}
			if t.Kind == model.KindDone && !n.isToday(p.LastEdited) {
				continue
			}
			out = append(out, t)
		}
		if !res.HasMore || res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	// order: kind (top, next, backlog, done), then due date, then creation time
	rank := map[string]int{model.KindTop: 0, model.KindNext: 1, model.KindBacklog: 2, model.KindDone: 3}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Kind] != rank[out[j].Kind] {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		if out[i].Due != out[j].Due {
			if out[i].Due == "" {
				return false
			}
			if out[j].Due == "" {
				return true
			}
			return out[i].Due < out[j].Due
		}
		return out[i].Sort < out[j].Sort
	})
	for i := range out {
		out[i].Sort = i
	}
	return out, nil
}

func (n *Notion) isToday(t time.Time) bool {
	tz := n.TZ
	if tz == nil {
		tz = time.Local
	}
	y, m, d := time.Now().In(tz).Date()
	ey, em, ed := t.In(tz).Date()
	return y == ey && m == em && d == ed
}

func (n *Notion) toTask(s *Schema, p notionPage) model.Task {
	t := model.Task{ID: p.ID, URL: p.URL, Sort: int(p.Created.Unix() % 1e9)}
	t.Title = propText(p.Properties[s.TitleProp])
	t.Status = propText(p.Properties[s.StatusProp])
	t.Kind = n.Mapping.Kind(t.Status)
	t.Top = t.Kind == model.KindTop
	t.Done = t.Kind == model.KindDone
	if s.Due != "" {
		t.Due = propDate(p.Properties[s.Due])
	}
	if s.Project != "" {
		t.Project = propText(p.Properties[s.Project])
	}
	if s.Estimate != "" {
		t.EstimateMin = int(propNumber(p.Properties[s.Estimate]))
	}
	if s.NextStep != "" {
		t.NextStep = propText(p.Properties[s.NextStep])
	}
	if t.Title == "" {
		t.Title = "(untitled)"
	}
	return t
}

// SetStatus changes the status of a page. Used for done (status = Done) and undo (previous status).
func (n *Notion) SetStatus(ctx context.Context, pageID, status string) error {
	s, err := n.LoadSchema(ctx)
	if err != nil {
		return err
	}
	statusType := s.Props[s.StatusProp]
	payload := map[string]any{"properties": map[string]any{s.StatusProp: map[string]any{statusType: map[string]any{"name": status}}}}
	return n.do(ctx, "PATCH", "/v1/pages/"+pageID, payload, nil)
}

/* ---------- property helpers: Notion wraps every value in a typed object ---------- */

func propText(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var p struct {
		Title       []struct{ PlainText string `json:"plain_text"` } `json:"title"`
		RichText    []struct{ PlainText string `json:"plain_text"` } `json:"rich_text"`
		Select      *struct{ Name string `json:"name"` }            `json:"select"`
		Status      *struct{ Name string `json:"name"` }            `json:"status"`
		MultiSelect []struct{ Name string `json:"name"` }           `json:"multi_select"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	var sb strings.Builder
	for _, t := range p.Title {
		sb.WriteString(t.PlainText)
	}
	for _, t := range p.RichText {
		sb.WriteString(t.PlainText)
	}
	if p.Select != nil {
		return p.Select.Name
	}
	if p.Status != nil {
		return p.Status.Name
	}
	if len(p.MultiSelect) > 0 {
		names := make([]string, 0, len(p.MultiSelect))
		for _, m := range p.MultiSelect {
			names = append(names, m.Name)
		}
		return strings.Join(names, ", ")
	}
	return sb.String()
}

func propDate(raw json.RawMessage) string {
	var p struct {
		Date *struct {
			Start string `json:"start"`
		} `json:"date"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Date == nil {
		return ""
	}
	if len(p.Date.Start) >= 10 {
		return p.Date.Start[:10]
	}
	return p.Date.Start
}

func propNumber(raw json.RawMessage) float64 {
	var p struct {
		Number *float64 `json:"number"`
	}
	_ = json.Unmarshal(raw, &p)
	if p.Number == nil {
		return 0
	}
	return *p.Number
}

// DummyTasks is used when Notion is not configured.
func DummyTasks() []model.Task {
	mk := func(id, title, status, project string, est int, next string, sort int) model.Task {
		m := DefaultMapping()
		k := m.Kind(status)
		return model.Task{ID: id, Title: title, Status: status, Kind: k, Top: k == model.KindTop, Done: k == model.KindDone, Project: project, EstimateMin: est, NextStep: next, Sort: sort}
	}
	return []model.Task{
		mk("t1", "Write backend API spec for dashboard", "Priority", "Kindle app", 90, "Open the Notion page and list the 6 endpoints", 0),
		mk("t2", "Review TreeMapper pull request #214", "Done", "ForestCloud", 45, "Read the diff of sync.ts first", 1),
		mk("t3", "Reply to Academy host in Nagpur", "Priority", "Academies", 15, "Confirm the date and send the checklist", 2),
		mk("t4", "Order new strings for the guitar", "In Progress", "Personal", 10, "Amazon basket already has them", 3),
		mk("t5", "Book train for Diwali trip", "In Progress", "Personal", 20, "Check 12 Nov evening trains", 4),
		mk("t6", "Prepare slides for Monday standup", "All Task", "ForestCloud", 30, "Copy last week's deck", 5),
		mk("t7", "Read the TFFF briefing", "All Task", "Planet", 40, "", 6),
		mk("t8", "Old idea: dashboard widgets", "Parked", "Personal", 0, "", 7),
	}
}
