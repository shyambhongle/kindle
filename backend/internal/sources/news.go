package sources

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/shyam/dashboard/internal/model"
)

type Feed struct {
	URL  string
	Name string
}

var tagRe = regexp.MustCompile(`<[^>]*>`)
var spaceRe = regexp.MustCompile(`\s+`)

// News fetches each feed, keeps the newest items, and strips HTML from summaries.
func News(ctx context.Context, feeds []Feed, limit int) ([]model.News, map[string]error) {
	errs := map[string]error{}
	parser := gofeed.NewParser()
	parser.Client = Client
	parser.UserAgent = "kindle-dashboard/1.0"
	type item struct {
		n  model.News
		at time.Time
	}
	var items []item
	for _, f := range feeds {
		fctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		feed, err := parser.ParseURLWithContext(f.URL, fctx)
		cancel()
		if err != nil {
			errs[f.URL] = err
			continue
		}
		name := f.Name
		if name == "" {
			name = feed.Title
		}
		for i, it := range feed.Items {
			if i >= 8 {
				break
			}
			at := time.Time{}
			if it.PublishedParsed != nil {
				at = *it.PublishedParsed
			} else if it.UpdatedParsed != nil {
				at = *it.UpdatedParsed
			}
			h := sha1.Sum([]byte(it.Link + it.Title))
			items = append(items, item{at: at, n: model.News{
				ID:      hex.EncodeToString(h[:8]),
				Source:  name,
				Title:   clean(it.Title),
				Summary: truncate(clean(it.Description), 220),
			}})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.After(items[j].at) })
	out := make([]model.News, 0, limit)
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.n.ID] {
			continue
		}
		seen[it.n.ID] = true
		out = append(out, it.n)
		if len(out) >= limit {
			break
		}
	}
	return out, errs
}

func clean(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&quot;", `"`, "&#39;", "'", "&lt;", "<", "&gt;", ">").Replace(s)
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n], " ")
	if cut < n/2 {
		cut = n
	}
	return s[:cut] + "..."
}

func DummyNews() []model.News {
	return []model.News{
		{ID: "n1", Source: "Reuters", Title: "EU agrees on new forest restoration targets for 2030", Summary: "Member states back a plan to restore 20 percent of degraded land, with funding tied to monitoring."},
		{ID: "n2", Source: "The Hindu", Title: "Pune metro line 3 to open first stretch in December", Summary: "The Hinjewadi to Shivajinagar corridor will start partial service."},
		{ID: "n3", Source: "Ars Technica", Title: "E-ink maker shows 300 ppi color panel with faster refresh", Summary: "The new panel halves refresh time compared to the current generation."},
		{ID: "n4", Source: "BBC", Title: "Scientists map tree cover change across the Western Ghats", Summary: "A ten year satellite study shows gains in protected areas and losses near cities."},
	}
}
