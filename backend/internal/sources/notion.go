package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shyam/dashboard/internal/model"
)

// Notion reads tasks from a Notion database using an internal integration token.
//
// Expected properties in the database (names are configurable here):
//   Name (title), Done (checkbox), Top (checkbox), Project (select or text),
//   Estimate (number, minutes), Next step (rich text), Due (date, optional)
type Notion struct {
	Token string
	DBID  string
	TZ    *time.Location
	// property names
	PropTitle, PropDone, PropTop, PropProject, PropEstimate, PropNext string
}

func NewNotion(token, dbID string, tz *time.Location) *Notion {
	return &Notion{Token: token, DBID: dbID, TZ: tz,
		PropTitle: "Name", PropDone: "Done", PropTop: "Top", PropProject: "Project", PropEstimate: "Estimate", PropNext: "Next step"}
}

func (n *Notion) Configured() bool { return n.Token != "" && n.DBID != "" }

func (n *Notion) Tasks(ctx context.Context) ([]model.Task, error) {
	body := map[string]any{
		"page_size": 50,
		"filter":    map[string]any{"property": n.PropDone, "checkbox": map[string]any{"equals": false}},
		"sorts":     []map[string]any{{"timestamp": "created_time", "direction": "ascending"}},
	}
	// Also include tasks done today so the "done today" count works. Notion has no "edited today" filter
	// on checkboxes, so we fetch done=true sorted by last edit, limited, and the collector keeps today's.
	var out []model.Task
	for i, filter := range []map[string]any{
		{"property": n.PropDone, "checkbox": map[string]any{"equals": false}},
		{"property": n.PropDone, "checkbox": map[string]any{"equals": true}},
	} {
		body["filter"] = filter
		if i == 1 {
			body["page_size"] = 10
			body["sorts"] = []map[string]any{{"timestamp": "last_edited_time", "direction": "descending"}}
		}
		res, err := n.query(ctx, body)
		if err != nil {
			return nil, err
		}
		out = append(out, res...)
	}
	for i := range out {
		out[i].Sort = i
	}
	return out, nil
}

// MarkDone sets the Done checkbox on a page. Best effort, called from the write endpoint.
func (n *Notion) MarkDone(ctx context.Context, pageID string, done bool) error {
	payload, _ := json.Marshal(map[string]any{"properties": map[string]any{n.PropDone: map[string]any{"checkbox": done}}})
	req, _ := http.NewRequestWithContext(ctx, "PATCH", "https://api.notion.com/v1/pages/"+pageID, bytes.NewReader(payload))
	n.headers(req)
	resp, err := Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("notion %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (n *Notion) headers(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+n.Token)
	req.Header.Set("Notion-Version", "2022-06-28")
	req.Header.Set("Content-Type", "application/json")
}

type notionPage struct {
	ID         string                     `json:"id"`
	LastEdited time.Time                  `json:"last_edited_time"`
	Properties map[string]json.RawMessage `json:"properties"`
}

func (n *Notion) query(ctx context.Context, body map[string]any) ([]model.Task, error) {
	payload, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.notion.com/v1/databases/"+n.DBID+"/query", bytes.NewReader(payload))
	n.headers(req)
	resp, err := Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("notion %d: %s", resp.StatusCode, b)
	}
	var res struct {
		Results []notionPage `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&res); err != nil {
		return nil, err
	}
	var tasks []model.Task
	for _, p := range res.Results {
		t := model.Task{ID: p.ID}
		t.Title = propText(p.Properties[n.PropTitle])
		t.Done = propBool(p.Properties[n.PropDone])
		t.Top = propBool(p.Properties[n.PropTop])
		t.Project = propText(p.Properties[n.PropProject])
		t.EstimateMin = int(propNumber(p.Properties[n.PropEstimate]))
		t.NextStep = propText(p.Properties[n.PropNext])
		if t.Title == "" {
			t.Title = "(untitled)"
		}
		// done tasks are only interesting if they were finished today
		if t.Done && n.TZ != nil {
			y, m, d := time.Now().In(n.TZ).Date()
			ey, em, ed := p.LastEdited.In(n.TZ).Date()
			if y != ey || m != em || d != ed {
				continue
			}
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// property helpers: Notion wraps every value in a typed object
func propText(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var p struct {
		Type     string `json:"type"`
		Title    []struct{ PlainText string `json:"plain_text"` } `json:"title"`
		RichText []struct{ PlainText string `json:"plain_text"` } `json:"rich_text"`
		Select   *struct{ Name string `json:"name"` } `json:"select"`
		Status   *struct{ Name string `json:"name"` } `json:"status"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	s := ""
	for _, t := range p.Title {
		s += t.PlainText
	}
	for _, t := range p.RichText {
		s += t.PlainText
	}
	if p.Select != nil {
		s = p.Select.Name
	}
	if p.Status != nil {
		s = p.Status.Name
	}
	return s
}

func propBool(raw json.RawMessage) bool {
	var p struct {
		Checkbox bool `json:"checkbox"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.Checkbox
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
	return []model.Task{
		{ID: "t1", Title: "Write backend API spec for dashboard", Project: "Kindle app", EstimateMin: 90, Top: true, NextStep: "Open the Notion page and list the 6 endpoints", Sort: 0},
		{ID: "t2", Title: "Review TreeMapper pull request #214", Project: "ForestCloud", EstimateMin: 45, Top: true, Done: true, NextStep: "Read the diff of sync.ts first", Sort: 1},
		{ID: "t3", Title: "Reply to Academy host in Nagpur", Project: "Academies", EstimateMin: 15, Top: true, NextStep: "Confirm the date and send the checklist", Sort: 2},
		{ID: "t4", Title: "Order new strings for the guitar", Project: "Personal", EstimateMin: 10, NextStep: "Amazon basket already has them", Sort: 3},
		{ID: "t5", Title: "Book train for Diwali trip", Project: "Personal", EstimateMin: 20, NextStep: "Check 12 Nov evening trains", Sort: 4},
		{ID: "t6", Title: "Prepare slides for Monday standup", Project: "ForestCloud", EstimateMin: 30, NextStep: "Copy last week's deck", Sort: 5},
	}
}
