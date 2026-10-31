// Package importer loads the data/ folder into the content tables. Safe to run repeatedly.
package importer

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
)

type guitarWeek struct {
	Week      int      `yaml:"week"`
	Lesson    string   `yaml:"lesson"`
	Exercises []string `yaml:"exercises"`
	Notes     string   `yaml:"notes"`
}

type book struct {
	Title     string `yaml:"title"`
	Author    string `yaml:"author"`
	Pages     int    `yaml:"pages"`
	Status    string `yaml:"status"`
	StartedOn string `yaml:"started_on"`
}

// Run imports every dataset found in dataDir. Missing files are skipped with a log line.
func Run(ctx context.Context, pool *pgxpool.Pool, dataDir string) error {
	steps := []struct {
		name string
		fn   func(context.Context, *pgxpool.Pool, string) (int, error)
		path string
	}{
		{"guitar plan", importGuitar, filepath.Join(dataDir, "guitar", "plan.yaml")},
		{"german sentences", importGermanSentences, filepath.Join(dataDir, "german", "sentences.csv")},
		{"german vocab", importGermanVocab, filepath.Join(dataDir, "german", "vocab.csv")},
		{"english vocab", importVocab, filepath.Join(dataDir, "vocab", "words.csv")},
		{"books", importBooks, filepath.Join(dataDir, "books", "books.yaml")},
		{"news sources", importNews, filepath.Join(dataDir, "news", "sources.txt")},
		{"german course", importCourse, filepath.Join(dataDir, "german", "course.yaml")},
	}
	for _, s := range steps {
		if _, err := os.Stat(s.path); err != nil {
			log.Printf("import: %s: skipped (%s not found)", s.name, s.path)
			continue
		}
		n, err := s.fn(ctx, pool, s.path)
		if err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		log.Printf("import: %s: %d rows", s.name, n)
	}
	// make sure every practice key has a state row, and every review item has a due row
	for _, k := range []string{"guitar", "german", "vocab", "reading"} {
		if _, err := pool.Exec(ctx, `INSERT INTO practice_state(key) VALUES($1) ON CONFLICT DO NOTHING`, k); err != nil {
			return err
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reviews(kind,item_id) SELECT 'german', id FROM german_items WHERE kind='word' ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reviews(kind,item_id) SELECT 'vocab', id FROM vocab_items ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	return nil
}

func importGuitar(ctx context.Context, pool *pgxpool.Pool, path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var weeks []guitarWeek
	if err := yaml.Unmarshal(raw, &weeks); err != nil {
		return 0, err
	}
	n := 0
	for _, w := range weeks {
		if w.Week <= 0 {
			continue
		}
		ex, _ := json.Marshal(w.Exercises)
		if _, err := pool.Exec(ctx, `INSERT INTO guitar_plan(week,lesson,exercises,notes) VALUES($1,$2,$3,$4)
			ON CONFLICT (week) DO UPDATE SET lesson=EXCLUDED.lesson, exercises=EXCLUDED.exercises, notes=EXCLUDED.notes`,
			w.Week, w.Lesson, string(ex), w.Notes); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func readCSV(path string) ([]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	var rows []map[string]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		m := map[string]string{}
		for i, h := range header {
			if i < len(rec) {
				m[strings.TrimSpace(h)] = strings.TrimSpace(rec[i])
			}
		}
		rows = append(rows, m)
	}
	return rows, nil
}

func importGermanSentences(ctx context.Context, pool *pgxpool.Pool, path string) (int, error) {
	return importGerman(ctx, pool, path, "sentence")
}
func importGermanVocab(ctx context.Context, pool *pgxpool.Pool, path string) (int, error) {
	return importGerman(ctx, pool, path, "word")
}
func importGerman(ctx context.Context, pool *pgxpool.Pool, path, kind string) (int, error) {
	rows, err := readCSV(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		if r["de"] == "" {
			continue
		}
		if _, err := pool.Exec(ctx, `INSERT INTO german_items(kind,de,en) VALUES($1,$2,$3)
			ON CONFLICT (kind,de) DO UPDATE SET en=EXCLUDED.en`, kind, r["de"], r["en"]); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func importVocab(ctx context.Context, pool *pgxpool.Pool, path string) (int, error) {
	rows, err := readCSV(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		if r["word"] == "" {
			continue
		}
		if _, err := pool.Exec(ctx, `INSERT INTO vocab_items(word,pos,definition,example) VALUES($1,$2,$3,$4)
			ON CONFLICT (word) DO UPDATE SET pos=EXCLUDED.pos, definition=EXCLUDED.definition, example=EXCLUDED.example`,
			r["word"], r["pos"], r["definition"], r["example"]); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func importBooks(ctx context.Context, pool *pgxpool.Pool, path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var books []book
	if err := yaml.Unmarshal(raw, &books); err != nil {
		return 0, err
	}
	n := 0
	for _, b := range books {
		if b.Title == "" {
			continue
		}
		if b.Status == "" {
			b.Status = "next"
		}
		var started *string
		if b.StartedOn != "" {
			started = &b.StartedOn
		}
		if _, err := pool.Exec(ctx, `INSERT INTO books(title,author,pages,status,started_on) VALUES($1,$2,$3,$4,$5::date)
			ON CONFLICT (title) DO UPDATE SET author=EXCLUDED.author, pages=EXCLUDED.pages, status=EXCLUDED.status, started_on=EXCLUDED.started_on`,
			b.Title, b.Author, b.Pages, b.Status, started); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func importNews(ctx context.Context, pool *pgxpool.Pool, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		url := strings.TrimSpace(parts[0])
		name, category := "", "general"
		if len(parts) > 1 {
			name = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 && strings.TrimSpace(parts[2]) == "security" {
			category = "security"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO news_sources(url,name,category) VALUES($1,$2,$3) ON CONFLICT (url) DO UPDATE SET name=EXCLUDED.name, category=EXCLUDED.category`, url, name, category); err != nil {
			return n, err
		}
		n++
	}
	return n, sc.Err()
}

type courseFile struct {
	StartDate  string            `yaml:"start_date"`
	ExamTarget string            `yaml:"exam_target"`
	Weeks      []courseWeek      `yaml:"weeks"`
	QuizDesign map[string]string `yaml:"quiz_design"`
	Written    map[string]string `yaml:"written_design"`
	Statements []string          `yaml:"discussion_statements"`
}

type courseWeek struct {
	Week       int    `yaml:"week"`
	Phase      string `yaml:"phase"`
	Grammar    string `yaml:"grammar"`
	Vocab      string `yaml:"vocab"`
	Phrases    string `yaml:"phrases"`
	Speaking   string `yaml:"speaking"`
	Saturday   string `yaml:"saturday"`
	Checkpoint string `yaml:"checkpoint"`
}

// importCourse loads the week plan and the course settings (start date, test designs, statements).
func importCourse(ctx context.Context, pool *pgxpool.Pool, path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var c courseFile
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return 0, err
	}
	n := 0
	for _, w := range c.Weeks {
		if w.Week <= 0 {
			continue
		}
		if _, err := pool.Exec(ctx, `INSERT INTO german_weeks(week,phase,grammar,vocab,phrases,speaking,saturday,checkpoint) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (week) DO UPDATE SET phase=EXCLUDED.phase, grammar=EXCLUDED.grammar, vocab=EXCLUDED.vocab, phrases=EXCLUDED.phrases,
			speaking=EXCLUDED.speaking, saturday=EXCLUDED.saturday, checkpoint=EXCLUDED.checkpoint`,
			w.Week, w.Phase, w.Grammar, w.Vocab, w.Phrases, w.Speaking, w.Saturday, w.Checkpoint); err != nil {
			return n, err
		}
		n++
	}
	settings := map[string]any{"start_date": c.StartDate, "exam_target": c.ExamTarget, "quiz_design": c.QuizDesign, "written_design": c.Written, "statements": c.Statements}
	js, _ := json.Marshal(settings)
	if _, err := pool.Exec(ctx, `INSERT INTO kv(key,value,updated_at) VALUES('german_course',$1,now()) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, js); err != nil {
		return n, err
	}
	return n, nil
}

/* ---------- book summary plan ---------- */

type summaryPlanFile struct {
	StartDate  string            `yaml:"start_date"`
	Words      int               `yaml:"words"`
	Style      string            `yaml:"style"`
	Language   string            `yaml:"language"`
	Priority   string            `yaml:"priority"`
	Reader     string            `yaml:"reader"`
	Categories map[string]string `yaml:"categories"`
	Books      []struct {
		Title    string `yaml:"title"`
		Author   string `yaml:"author"`
		Category string `yaml:"category"`
		Why      string `yaml:"why"`
	} `yaml:"books"`
}

// SeedBookPlan loads data/books/summaries.yaml into book_plan, but only while book_plan is
// empty: after the first load the plan lives in Postgres and is edited on the dashboard.
// The settings row is written only if it does not exist yet.
func SeedBookPlan(ctx context.Context, pool *pgxpool.Pool, dataDir string) (int, error) {
	path := filepath.Join(dataDir, "books", "summaries.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var f summaryPlanFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	settings := map[string]any{"start_date": f.StartDate, "words": f.Words, "style": f.Style, "language": f.Language,
		"priority": f.Priority, "reader": f.Reader, "categories": f.Categories}
	js, _ := json.Marshal(settings)
	if _, err := pool.Exec(ctx, `INSERT INTO kv(key,value,updated_at) VALUES('book_summaries',$1,now()) ON CONFLICT (key) DO NOTHING`, js); err != nil {
		return 0, err
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM book_plan`).Scan(&count); err != nil {
		return 0, err
	}
	if count > 0 {
		return 0, nil
	}
	n := 0
	for _, b := range f.Books {
		if strings.TrimSpace(b.Title) == "" {
			continue
		}
		n++
		if _, err := pool.Exec(ctx, `INSERT INTO book_plan(position,title,author,category,why) VALUES($1,$2,$3,$4,$5)`, n, b.Title, b.Author, b.Category, b.Why); err != nil {
			return n, err
		}
	}
	return n, nil
}
