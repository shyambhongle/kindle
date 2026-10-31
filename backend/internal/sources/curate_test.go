package sources

import (
	"testing"

	"github.com/shyam/dashboard/internal/model"
)

func titles(items []model.News) []string {
	out := make([]string, 0, len(items))
	for _, n := range items {
		out = append(out, n.Title)
	}
	return out
}

func has(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// Hacker News is its own category but is still keyword filtered, the same as a general feed.
// Security is the only category that passes through untouched.
func TestCurateFiltersEveryCategoryExceptSecurity(t *testing.T) {
	feeds := []Feed{
		{URL: "hn", Name: "Hacker News", Category: "hn", Mode: "keywords", MaxItems: 10},
		{URL: "gen", Name: "General", Category: "general", Mode: "keywords", MaxItems: 10},
		{URL: "sec", Name: "Socket", Category: "security", Mode: "all", MaxItems: 10},
	}
	items := []model.News{
		{Title: "New open source database release", Category: "hn", FeedURL: "hn"},
		{Title: "Cricket board announces new captain", Category: "hn", FeedURL: "hn"},
		{Title: "GitHub ships ARM runners", Category: "general", FeedURL: "gen"},
		{Title: "Minister opens new parliament session", Category: "general", FeedURL: "gen"},
		{Title: "Cricket squad named for the series", Category: "security", FeedURL: "sec"},
	}
	kept, muted := Curate(items, feeds, DefaultFilter(), 24, 15)
	got := titles(kept)

	for _, want := range []string{"New open source database release", "GitHub ships ARM runners"} {
		if !has(got, want) {
			t.Errorf("expected %q to be kept, got %v", want, got)
		}
	}
	for _, drop := range []string{"Cricket board announces new captain", "Minister opens new parliament session"} {
		if has(got, drop) {
			t.Errorf("expected %q to be filtered out, got %v", drop, got)
		}
	}
	// a security item is never judged by the keyword filter, however off topic it reads
	if !has(got, "Cricket squad named for the series") {
		t.Errorf("security items must pass the filter untouched, got %v", got)
	}
	if len(muted) != 2 {
		t.Errorf("expected 2 muted items, got %d: %v", len(muted), muted)
	}
}

func TestCurateAppliesPerFeedAndPerCategoryCaps(t *testing.T) {
	feeds := []Feed{
		{URL: "hn", Category: "hn", Mode: "all", MaxItems: 2},
		{URL: "gen", Category: "general", Mode: "all", MaxItems: 10},
	}
	var items []model.News
	for i := 0; i < 5; i++ {
		items = append(items, model.News{Title: "hn item", Category: "hn", FeedURL: "hn"})
		items = append(items, model.News{Title: "general item", Category: "general", FeedURL: "gen"})
	}
	kept, _ := Curate(items, feeds, Filter{}, 3, 15)

	var hn, general int
	for _, n := range kept {
		switch n.Category {
		case "hn":
			hn++
		case "general":
			general++
		}
	}
	if hn != 2 {
		t.Errorf("per feed cap of 2 not applied to hn, got %d", hn)
	}
	// hn and general count against their own category limits, so general is capped at 3, not 1
	if general != 3 {
		t.Errorf("category limit of 3 not applied to general, got %d", general)
	}
}
