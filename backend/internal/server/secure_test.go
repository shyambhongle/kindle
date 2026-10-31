package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// secure() sets the headers every response carries and the body limit for each area.
func TestSecureHeadersAndBodyLimits(t *testing.T) {
	var readErr error
	h := secure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("always on", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/d/tok/api/dashboard", nil))
		for k, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "no-referrer",
			"Cache-Control":          "no-store",
		} {
			if got := rec.Header().Get(k); got != want {
				t.Errorf("%s = %q, want %q", k, got, want)
			}
		}
	})

	// Referrer-Policy matters more than usual here: the access token is in the URL path, so
	// without it any outbound link would hand the token to another site.
	t.Run("admin gets a content security policy", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/news", nil))
		csp := rec.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Fatal("no Content-Security-Policy on an admin response")
		}
		for _, want := range []string{"default-src 'none'", "style-src 'self' 'unsafe-inline'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("CSP missing %q, got %q", want, csp)
			}
		}
		if strings.Contains(csp, "script-src") {
			t.Errorf("the admin has no scripts, so default-src 'none' should cover it: %q", csp)
		}
	})

	t.Run("kindle bodies are capped at 4KB", func(t *testing.T) {
		body := strings.NewReader(strings.Repeat("a", 5<<10))
		rec := httptest.NewRecorder()
		readErr = nil
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/d/tok/api/tasks/1/done", body))
		if readErr == nil {
			t.Error("a 5KB body on a Kindle route should have been refused")
		}
	})

	t.Run("admin bodies allow a pasted CSV", func(t *testing.T) {
		body := strings.NewReader(strings.Repeat("a", 64<<10))
		rec := httptest.NewRecorder()
		readErr = nil
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/vocab/import", body))
		if readErr != nil {
			t.Errorf("a 64KB paste into the dashboard should be allowed, got %v", readErr)
		}
	})
}

func TestMaskToken(t *testing.T) {
	long := strings.Repeat("a", 64)
	got := maskToken(long)
	if strings.Contains(long, got) && len(got) >= len(long) {
		t.Fatal("the mask must not reproduce the token")
	}
	if got != "aaaaaa..." {
		t.Errorf("maskToken(64 chars) = %q", got)
	}
	if maskToken("short") != "*****" {
		t.Errorf("a short token must be fully hidden, got %q", maskToken("short"))
	}
	if maskToken("") != "" {
		t.Errorf("maskToken(\"\") = %q", maskToken(""))
	}
}
