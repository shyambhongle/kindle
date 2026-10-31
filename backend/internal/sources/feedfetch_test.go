package sources

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

const sampleFeed = `<?xml version="1.0"?><rss version="2.0"><channel>
<title>Test feed</title>
<item><title>GitHub ships a thing</title><link>https://example.com/1</link><description>A release.</description></item>
</channel></rss>`

// A feed served from the loopback must be refused. This is the whole point of FeedClient:
// feed URLs come from the dashboard, so without it a feed could be aimed at anything else
// listening on the Mac, and a redirect from a public feed could do the same.
func TestFeedClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleFeed))
	}))
	defer srv.Close()

	_, err := fetchFeed(context.Background(), gofeed.NewParser(), srv.URL)
	if err == nil {
		t.Fatal("expected the loopback address to be refused, got no error")
	}
	if !strings.Contains(err.Error(), "refusing to connect") {
		t.Fatalf("expected a refusal from the dial guard, got: %v", err)
	}
}

// Redirects are covered by the same guard, because it hangs off the transport's DialContext
// rather than off the URL that was typed in: every hop in a redirect chain has to dial, and
// every dial is vetted. This checks the guard directly, since a unit test cannot stand up a
// genuinely public host that redirects inward.
func TestGuardedDialRefusesEveryHop(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleFeed))
	}))
	defer target.Close()

	host := strings.TrimPrefix(target.URL, "http://")
	if _, err := guardedDial(context.Background(), "tcp", host); err == nil {
		t.Fatalf("guardedDial(%s) should have been refused", host)
	}
	// and the transport the feed client uses is the guarded one
	tr, ok := FeedClient.Transport.(*http.Transport)
	if !ok || tr.DialContext == nil {
		t.Fatal("FeedClient must use a transport with the guarded DialContext")
	}
}

// With the guard off, the same fetch works, so the guard is the only thing blocking it and
// fetchFeed parses a normal feed correctly.
func TestFetchFeedParsesWhenAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "kindle-dashboard/1.0" {
			t.Errorf("User-Agent = %q", got)
		}
		w.Write([]byte(sampleFeed))
	}))
	defer srv.Close()

	AllowPrivateFeedHosts = true
	defer func() { AllowPrivateFeedHosts = false }()

	feed, err := fetchFeed(context.Background(), gofeed.NewParser(), srv.URL)
	if err != nil {
		t.Fatalf("fetchFeed: %v", err)
	}
	if len(feed.Items) != 1 || feed.Items[0].Title != "GitHub ships a thing" {
		t.Fatalf("unexpected feed contents: %+v", feed)
	}
}

// An endless feed must not be read forever. maxBody bounds it, so the call returns an error
// instead of growing until the timeout or worse.
func TestFetchFeedCapsTheBody(t *testing.T) {
	var sent int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>x</title>`))
		chunk := []byte(strings.Repeat("<item><title>pad</title></item>", 1000))
		for sent < 32<<20 { // far more than maxBody
			n, err := w.Write(chunk)
			sent += int64(n)
			if err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	AllowPrivateFeedHosts = true
	defer func() { AllowPrivateFeedHosts = false }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := fetchFeed(ctx, gofeed.NewParser(), srv.URL); err == nil {
		t.Fatal("expected a truncated feed to fail to parse")
	}
	if sent > 3*maxBody {
		t.Errorf("read far past the cap: server sent %d bytes for a %d byte cap", sent, maxBody)
	}
	t.Logf("stopped after %d bytes in %s (cap %d)", sent, time.Since(start).Round(time.Millisecond), maxBody)
}

func TestInternalReason(t *testing.T) {
	blocked := []string{"127.0.0.1", "::1", "10.1.2.3", "192.168.1.20", "172.16.0.1",
		"169.254.169.254", "0.0.0.0", "100.64.0.1", "224.0.0.1", "240.0.0.1", "fc00::1", "fe80::1"}
	for _, s := range blocked {
		if why := internalReason(net.ParseIP(s)); why == "" {
			t.Errorf("%s should be blocked, it was allowed", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700:4700::1111"} {
		if why := internalReason(net.ParseIP(s)); why != "" {
			t.Errorf("%s should be allowed, got %q", s, why)
		}
	}
}

func TestCheckFeedURL(t *testing.T) {
	for _, bad := range []string{"", "not a url", "ftp://example.com/f.xml", "file:///etc/passwd",
		"http://127.0.0.1:8080/feed", "http://localhost/feed", "https://169.254.169.254/latest/meta-data/"} {
		if err := CheckFeedURL(context.Background(), bad); err == nil {
			t.Errorf("CheckFeedURL(%q) should have failed", bad)
		}
	}
}
