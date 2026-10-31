// Package server is the HTTP layer. Every route lives under /d/{token}/ so a guest on the
// Wi-Fi cannot find the dashboard by port scanning. The Kindle browser cannot send headers,
// which is why the token is in the path.
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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shyam/dashboard/internal/collector"
	"github.com/shyam/dashboard/internal/config"
)

type Server struct {
	cfg  *config.Config
	col  *collector.Collector
	mux  *http.ServeMux
	page []byte // client index.html with apiUrl injected
}

func New(cfg *config.Config, col *collector.Collector) (*Server, error) {
	s := &Server{cfg: cfg, col: col, mux: http.NewServeMux()}
	raw, err := os.ReadFile(filepath.Join(cfg.ClientDir, "index.html"))
	if err != nil {
		return nil, err
	}
	s.page = []byte(strings.Replace(string(raw), `apiUrl: ""`, `apiUrl: "/d/`+cfg.AccessToken+`/api"`, 1))

	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	s.mux.HandleFunc("GET /d/{token}/", s.auth(s.client))
	s.mux.HandleFunc("GET /d/{token}/api/dashboard", s.auth(s.dashboard))
	s.mux.HandleFunc("POST /d/{token}/api/collect", s.auth(s.collect))
	s.mux.HandleFunc("POST /d/{token}/api/tasks/{id}/done", s.auth(s.taskDone))
	s.mux.HandleFunc("POST /d/{token}/api/practice/{key}/done", s.auth(s.practiceDone))
	s.mux.HandleFunc("POST /d/{token}/api/practice/guitar/advance", s.auth(s.guitarAdvance))
	s.mux.HandleFunc("POST /d/{token}/api/reading/sessions", s.auth(s.readingSession))
	return s, nil
}

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
	log.Printf("serving on http://%s/d/%s/", s.cfg.ListenAddr, s.cfg.AccessToken)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

/* ---------- middleware ---------- */

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10) // writes are tiny JSON bodies
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

func (s *Server) client(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(s.page)
}

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
