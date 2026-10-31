// Package server is the HTTP layer for the Kindle. Every route lives under /d/{token}/ so a
// guest on the Wi-Fi cannot find the dashboard by port scanning. The token is in the path
// because the KOReader plugin reads it from one configured URL.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shyam/dashboard/internal/collector"
	"github.com/shyam/dashboard/internal/config"
)

type Server struct {
	cfg *config.Config
	col *collector.Collector
	mux *http.ServeMux
}

func New(cfg *config.Config, col *collector.Collector) *Server {
	s := &Server{cfg: cfg, col: col, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	s.mux.HandleFunc("GET /d/{token}/api/dashboard", s.auth(s.dashboard))
	s.mux.HandleFunc("POST /d/{token}/api/collect", s.auth(s.collect))
	s.mux.HandleFunc("POST /d/{token}/api/tasks/{id}/done", s.auth(s.taskDone))
	s.mux.HandleFunc("POST /d/{token}/api/practice/{key}/done", s.auth(s.practiceDone))
	s.mux.HandleFunc("POST /d/{token}/api/practice/guitar/advance", s.auth(s.guitarAdvance))
	s.mux.HandleFunc("POST /d/{token}/api/reading/sessions", s.auth(s.readingSession))
	s.mux.HandleFunc("POST /d/{token}/api/german/quiz/{day}/result", s.auth(s.germanQuizResult))
	s.mux.HandleFunc("POST /d/{token}/api/german/written/{week}/reading", s.auth(s.germanReadingScore))
	s.mux.HandleFunc("POST /d/{token}/api/english/{day}/done", s.auth(s.englishDone))
	s.mux.HandleFunc("POST /d/{token}/api/summaries/{id}/read", s.auth(s.summaryRead))
	return s
}

// Mount lets other packages (the admin UI) add routes to the same mux.
func (s *Server) Mount(register func(*http.ServeMux)) { register(s.mux) }

func (s *Server) Handler() http.Handler {
	return secure(s.mux)
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	host, _, _ := net.SplitHostPort(s.cfg.ListenAddr)
	if host == "" || host == "0.0.0.0" {
		log.Printf("warning: listening on all interfaces; set LISTEN_ADDR to your LAN IP (e.g. 192.168.1.20:8080) to be stricter")
	}
	// The access token is the only credential for both the Kindle API and /admin, and this line
	// goes to dashboard.log under launchd, so only a short prefix is logged. `dashboard url`
	// prints the full address when you need it.
	log.Printf("serving on http://%s/d/%s/  (run ./dashboard url for the full address)", s.cfg.ListenAddr, maskToken(s.cfg.AccessToken))
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// maskToken keeps the token out of log files while leaving enough to tell two tokens apart.
func maskToken(tok string) string {
	if len(tok) <= 8 {
		return strings.Repeat("*", len(tok))
	}
	return tok[:6] + "..."
}

/* ---------- middleware ---------- */

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		limit := int64(4 << 10) // Kindle writes are tiny JSON bodies
		if strings.HasPrefix(r.URL.Path, "/admin") {
			limit = 1 << 20 // pasted CSV imports
			// The admin has no JavaScript of its own: one same origin stylesheet and nothing
			// else. So nothing is allowed to run at all. Inline styles stay allowed because
			// the templates position things with style attributes. This is defence in depth;
			// html/template is what actually stops injected markup from a feed or a task title.
			h.Set("Content-Security-Policy",
				"default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
					"form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	want := []byte(s.cfg.AccessToken)
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.PathValue("token"))
		if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
			time.Sleep(300 * time.Millisecond) // slow down guessing
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

/* ---------- handlers ---------- */

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	raw, err := s.col.Snapshot(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

// collect forces a refresh of every external source (the Kindle's Refresh only reads the snapshot).
func (s *Server) collect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := s.col.Collect(ctx); err != nil {
		fail(w, 500, err)
		return
	}
	s.dashboard(w, r)
}

func (s *Server) taskDone(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Done *bool `json:"done"`
	}
	if err := readJSON(r, &body); err != nil || body.Done == nil {
		fail(w, 400, errors.New(`body must be {"done": true|false}`))
		return
	}
	if err := s.col.SetTaskDone(r.Context(), r.PathValue("id"), *body.Done); err != nil {
		fail(w, 404, err)
		return
	}
	s.rebuild(w, r)
}

func (s *Server) practiceDone(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Done *bool `json:"done"`
	}
	if err := readJSON(r, &body); err != nil || body.Done == nil {
		fail(w, 400, errors.New(`body must be {"done": true|false}`))
		return
	}
	if err := s.col.SetPracticeDone(r.Context(), r.PathValue("key"), *body.Done); err != nil {
		fail(w, 400, err)
		return
	}
	s.rebuild(w, r)
}

func (s *Server) guitarAdvance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Delta int `json:"delta"`
	}
	_ = readJSON(r, &body)
	if body.Delta == 0 {
		body.Delta = 1
	}
	if body.Delta < -52 || body.Delta > 52 {
		fail(w, 400, errors.New("delta out of range"))
		return
	}
	if _, err := s.col.AdvanceGuitarWeek(r.Context(), body.Delta); err != nil {
		fail(w, 500, err)
		return
	}
	s.rebuild(w, r)
}

func (s *Server) readingSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pages   int `json:"pages"`
		Minutes int `json:"minutes"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	if err := s.col.AddReadingSession(r.Context(), body.Pages, body.Minutes); err != nil {
		fail(w, 400, err)
		return
	}
	s.rebuild(w, r)
}

// germanQuizResult records the Kindle's answers to today's quiz and marks German practice done.
func (s *Server) germanQuizResult(w http.ResponseWriter, r *http.Request) {
	if s.col.Course == nil {
		fail(w, 404, errors.New("course not loaded"))
		return
	}
	var body struct {
		Answers []int `json:"answers"`
	}
	if err := readJSON(r, &body); err != nil || len(body.Answers) == 0 {
		fail(w, 400, errors.New(`body must be {"answers":[0,2,1,...]}`))
		return
	}
	day, _ := strconv.Atoi(r.PathValue("day"))
	if _, err := s.col.Course.RecordResult(r.Context(), day, body.Answers); err != nil {
		fail(w, 400, err)
		return
	}
	_ = s.col.SetPracticeDone(r.Context(), "german", true)
	s.rebuild(w, r)
}

// germanReadingScore stores the Kindle-graded reading part of the Saturday test.
func (s *Server) germanReadingScore(w http.ResponseWriter, r *http.Request) {
	if s.col.Course == nil {
		fail(w, 404, errors.New("course not loaded"))
		return
	}
	var body struct {
		Score int `json:"score"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	week, _ := strconv.Atoi(r.PathValue("week"))
	if err := s.col.Course.SetReadingScore(r.Context(), week, body.Score); err != nil {
		fail(w, 400, err)
		return
	}
	s.rebuild(w, r)
}

// englishDone marks the read-aloud ("read") or the speaking part ("speak") of a day's pack done.
func (s *Server) englishDone(w http.ResponseWriter, r *http.Request) {
	if s.col.English == nil {
		fail(w, 404, errors.New("english not loaded"))
		return
	}
	var body struct {
		Part string `json:"part"`
		Done *bool  `json:"done"`
	}
	if err := readJSON(r, &body); err != nil || body.Done == nil {
		fail(w, 400, errors.New(`body must be {"part":"read"|"speak","done":true|false}`))
		return
	}
	day, err := time.ParseInLocation("2006-01-02", r.PathValue("day"), s.cfg.Timezone)
	if err != nil {
		fail(w, 400, errors.New("day must be YYYY-MM-DD"))
		return
	}
	if err := s.col.English.SetDone(r.Context(), day, body.Part, *body.Done); err != nil {
		fail(w, 400, err)
		return
	}
	s.rebuild(w, r)
}

// summaryRead marks a book summary read (counts as today's reading practice) or unread.
func (s *Server) summaryRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Done *bool `json:"done"`
	}
	if err := readJSON(r, &body); err != nil || body.Done == nil {
		fail(w, 400, errors.New(`body must be {"done": true|false}`))
		return
	}
	id, _ := strconv.Atoi(r.PathValue("id"))
	if err := s.col.MarkSummaryRead(r.Context(), id, *body.Done); err != nil {
		fail(w, 400, err)
		return
	}
	s.rebuild(w, r)
}

func (s *Server) rebuild(w http.ResponseWriter, r *http.Request) {
	d, err := s.col.Build(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(d)
}

func readJSON(r *http.Request, v any) error {
	defer io.Copy(io.Discard, r.Body)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	if code >= 500 {
		log.Printf("http %d: %v", code, err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
