package sources

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/shyam/dashboard/internal/model"
)

type Feed struct {
	URL      string
	Name     string
	Category string // "general" | "hn" | "security"
	Mode     string // "keywords" | "all" (general feeds only)
	MaxItems int    // per-feed cap after filtering
}

type FeedResult struct {
	URL   string
	Items int
	Err   error
}

var tagRe = regexp.MustCompile(`<[^>]*>`)
var spaceRe = regexp.MustCompile(`\s+`)

// alertRe marks security items that describe an actual compromise rather than general advice.
var alertRe = regexp.MustCompile(`(?i)\b(malicious|malware|compromis|backdoor|supply[- ]chain attack|typosquat|infostealer|stealer|credential|exfiltrat|trojan|ransomware|worm|zero[- ]day|0[- ]day|exploit|hijack|poison|phishing|cve-\d{4}|remote code execution|rce\b|vulnerab|breach|leaked|takeover)`)

// IsAlert reports whether a headline or summary looks like a compromise worth flagging.
func IsAlert(title, summary string) bool {
	return alertRe.MatchString(title) || alertRe.MatchString(summary)
}

// News fetches each feed, keeps the newest items, and strips HTML from summaries.
// Every item carries its feed's category so the client can show two sections.
func News(ctx context.Context, feeds []Feed, limitPerCategory int) ([]model.News, []FeedResult) {
	parser := gofeed.NewParser()
	var items []model.News
	var results []FeedResult
	for _, f := range feeds {
		fctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		feed, err := fetchFeed(fctx, parser, f.URL)
		cancel()
		if err != nil {
			results = append(results, FeedResult{URL: f.URL, Err: err})
			continue
		}
		name := f.Name
		if name == "" {
			name = feed.Title
		}
		n := 0
		for i, it := range feed.Items {
			if i >= 20 {
				break
			}
			at := time.Time{}
			if it.PublishedParsed != nil {
				at = *it.PublishedParsed
			} else if it.UpdatedParsed != nil {
				at = *it.UpdatedParsed
			}
			h := sha1.Sum([]byte(it.Link + it.Title))
			title := clean(it.Title)
			summary := truncate(clean(firstNonEmpty(it.Description, it.Content)), 240)
			items = append(items, model.News{
				ID:          hex.EncodeToString(h[:8]),
				Source:      name,
				Title:       title,
				Summary:     summary,
				Link:        it.Link,
				Category:    f.Category,
				PublishedAt: at,
				Alert:       f.Category == "security" && IsAlert(title, summary),
				FeedURL:     f.URL,
			})
			n++
		}
		results = append(results, FeedResult{URL: f.URL, Items: n})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].PublishedAt.After(items[j].PublishedAt) })
	out := make([]model.News, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		out = append(out, it)
	}
	_ = limitPerCategory
	return out, results
}

// fetchFeed reads one feed through FeedClient, which will not connect to internal addresses,
// and caps the body at maxBody. gofeed's own ParseURL reads whatever the server sends, which
// a broken or hostile feed could turn into a lot of memory.
func fetchFeed(ctx context.Context, parser *gofeed.Parser, rawURL string) (*gofeed.Feed, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kindle-dashboard/1.0")
	req.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/xml;q=0.9, */*;q=0.8")
	resp, err := FeedClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
		resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return parser.Parse(io.LimitReader(resp.Body, maxBody))
}

// Muted is an item dropped by the filter, kept so you can tune the keywords.
type Muted struct {
	Title  string `json:"title"`
	Source string `json:"source"`
	Reason string `json:"reason"`
}

// Curate applies the filter and per-feed caps to everything except security items, which pass
// through (capped only). Returns the kept items and what was muted and why.
func Curate(items []model.News, feeds []Feed, f Filter, generalLimit, securityLimit int) ([]model.News, []Muted) {
	mode := map[string]string{}
	cap := map[string]int{}
	for _, fd := range feeds {
		mode[fd.URL] = fd.Mode
		cap[fd.URL] = fd.MaxItems
	}
	var kept []model.News
	var muted []Muted
	perFeed := map[string]int{}
	count := map[string]int{}
	for _, it := range items {
		if it.Category != "security" {
			if ok, why := f.Decide(it.Title, it.Summary, mode[it.FeedURL]); !ok {
				if len(muted) < 60 {
					muted = append(muted, Muted{Title: it.Title, Source: it.Source, Reason: why})
				}
				continue
			}
		}
		limit := cap[it.FeedURL]
		if limit <= 0 {
			limit = 5
		}
		if perFeed[it.FeedURL] >= limit {
			continue
		}
		catLimit := generalLimit
		if it.Category == "security" {
			catLimit = securityLimit
		}
		if count[it.Category] >= catLimit {
			continue
		}
		perFeed[it.FeedURL]++
		count[it.Category]++
		kept = append(kept, it)
	}
	return kept, muted
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func clean(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&quot;", `"`, "&#39;", "'", "&lt;", "<", "&gt;", ">", "&#x27;", "'").Replace(s)
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
	now := time.Now()
	mk := func(id, src, title, summary, cat string, hoursAgo int) model.News {
		return model.News{ID: id, Source: src, Title: title, Summary: summary, Category: cat, PublishedAt: now.Add(-time.Duration(hoursAgo) * time.Hour), Alert: cat == "security" && IsAlert(title, summary)}
	}
	return []model.News{
		mk("n1", "Reuters", "EU agrees on new forest restoration targets for 2030", "Member states back a plan to restore 20 percent of degraded land, with funding tied to monitoring.", "general", 2),
		mk("n2", "The Hindu", "Pune metro line 3 to open first stretch in December", "The Hinjewadi to Shivajinagar corridor will start partial service.", "general", 5),
		mk("n3", "Ars Technica", "E-ink maker shows 300 ppi color panel with faster refresh", "The new panel halves refresh time compared to the current generation.", "general", 9),
		mk("n4", "BBC", "Scientists map tree cover change across the Western Ghats", "A ten year satellite study shows gains in protected areas and losses near cities.", "general", 20),
		mk("s1", "Socket", "Malicious npm package impersonates a popular date library and steals tokens", "The typosquatted package ran a postinstall script that exfiltrated npm and GitHub tokens.", "security", 3),
		mk("s2", "Socket", "VS Code extension with 40k installs found pushing a backdoor", "The extension fetched a second stage from a Discord webhook after a silent update.", "security", 14),
		mk("s3", "Socket", "How we review dependency updates at Socket", "A walkthrough of our internal process for reviewing changes in third party code.", "security", 40),
		mk("s4", "Socket", "PyPI removes package that hijacked CI environment variables", "The package targeted GitHub Actions runners and uploaded secrets to a paste site.", "security", 60),
	}
}
