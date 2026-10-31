package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shyam/dashboard/internal/model"
)

// fakeNotion imitates the three endpoints the backend uses, with a board shaped like Shyam's.
func fakeNotion(t *testing.T, patches *[]string) *httptest.Server {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	page := func(id, title, status, due, edited string) map[string]any {
		props := map[string]any{
			"Task":   map[string]any{"type": "title", "title": []map[string]any{{"plain_text": title}}},
			"Status": map[string]any{"type": "status", "status": map[string]any{"name": status}},
		}
		if due != "" {
			props["Due"] = map[string]any{"type": "date", "date": map[string]any{"start": due}}
		}
		return map[string]any{"id": id, "url": "https://notion.so/" + id, "created_time": old, "last_edited_time": edited, "properties": props}
	}
	page1 := []map[string]any{
		page("a", "Write spec", "Priority", "2026-11-02", now),
		page("b", "Fix bug", "In Progress", "", now),
		page("c", "Old done", "Done", "", old),
		page("d", "Done today", "Done", "", now),
	}
	page2 := []map[string]any{
		page("e", "Urgent call", "Priority", "2026-11-01", now),
		page("f", "Someday", "All Task", "", old),
		page("g", "Strange status", "Weird", "", old),
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"message":"unauthorized"}`)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/databases/db1":
			json.NewEncoder(w).Encode(map[string]any{
				"title": []map[string]any{{"plain_text": "Shyam Tasks"}},
				"properties": map[string]any{
					"Task":   map[string]any{"type": "title"},
					"Status": map[string]any{"type": "status", "status": map[string]any{"options": []map[string]any{{"name": "All Task"}, {"name": "Parked"}, {"name": "In Progress"}, {"name": "Priority"}, {"name": "Done"}, {"name": "Weird"}}}},
					"Due":    map[string]any{"type": "date"},
					"Notes":  map[string]any{"type": "rich_text"},
				},
			})
		case r.Method == "POST" && r.URL.Path == "/v1/databases/db1/query":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f := body["filter"].(map[string]any)
			if f["property"] != "Status" || f["status"].(map[string]any)["does_not_equal"] != "Parked" {
				t.Errorf("unexpected filter: %v", f)
			}
			if body["start_cursor"] == "c2" {
				json.NewEncoder(w).Encode(map[string]any{"results": page2, "has_more": false})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"results": page1, "has_more": true, "next_cursor": "c2"})
			}
		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/v1/pages/"):
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			name := body["properties"].(map[string]any)["Status"].(map[string]any)["status"].(map[string]any)["name"]
			*patches = append(*patches, strings.TrimPrefix(r.URL.Path, "/v1/pages/")+"="+name.(string))
			fmt.Fprint(w, `{}`)
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestNotionTasks(t *testing.T) {
	var patches []string
	srv := fakeNotion(t, &patches)
	defer srv.Close()
	tz, _ := time.LoadLocation("Asia/Kolkata")
	n := NewNotion("secret", "db1", tz, DefaultMapping())
	n.BaseURL = srv.URL

	s, err := n.LoadSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.TitleProp != "Task" || s.StatusProp != "Status" || s.Due != "Due" || len(s.Options) != 6 {
		t.Fatalf("schema: %+v", s)
	}

	tasks, err := n.Tasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]model.Task{}
	for _, x := range tasks {
		got[x.ID] = x
	}
	if _, ok := got["c"]; ok {
		t.Error("done task edited 3 days ago should be dropped")
	}
	if got["d"].Kind != model.KindDone || !got["d"].Done {
		t.Errorf("d should be done today: %+v", got["d"])
	}
	if !got["a"].Top || got["a"].Kind != model.KindTop || got["a"].Due != "2026-11-02" {
		t.Errorf("a: %+v", got["a"])
	}
	if got["b"].Kind != model.KindNext || got["b"].Top {
		t.Errorf("b: %+v", got["b"])
	}
	if got["f"].Kind != model.KindBacklog || got["g"].Kind != model.KindBacklog {
		t.Errorf("backlog/unknown: %+v %+v", got["f"], got["g"])
	}
	// order: top sorted by due (e before a), then next, then backlog, then done
	want := []string{"e", "a", "b", "f", "g", "d"}
	for i, id := range want {
		if tasks[i].ID != id {
			t.Fatalf("order: got %v", ids(tasks))
		}
	}

	if err := n.SetStatus(context.Background(), "a", "Done"); err != nil {
		t.Fatal(err)
	}
	if len(patches) != 1 || patches[0] != "a=Done" {
		t.Fatalf("patches: %v", patches)
	}
}

func TestNotionBadToken(t *testing.T) {
	var patches []string
	srv := fakeNotion(t, &patches)
	defer srv.Close()
	n := NewNotion("wrong", "db1", time.UTC, DefaultMapping())
	n.BaseURL = srv.URL
	if _, err := n.LoadSchema(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

func TestMappingIsCaseInsensitive(t *testing.T) {
	m := DefaultMapping()
	if m.Kind("priority") != model.KindTop || m.Kind("in progress ") != model.KindNext || m.Kind("PARKED") != model.KindHidden {
		t.Fatal("mapping should ignore case and surrounding spaces")
	}
}

func ids(ts []model.Task) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}
