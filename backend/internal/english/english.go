// Package english generates the daily English pack: phrases you can use in day-to-day talk,
// a speaking topic for 2 to 3 minutes before bed, and a 5 minute passage to read aloud.
// Packs are generated on demand by the Claude API (one per calendar day), stored in Postgres,
// and shown on the Kindle. Themes and focus are set on the web dashboard.
package english

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shyam/dashboard/internal/llm"
)

/* ---------- types ---------- */

// Phrase is one expression worth adding to your active vocabulary.
type Phrase struct {
	Phrase   string `json:"phrase"`
	Meaning  string `json:"meaning"`
	Example  string `json:"example"`
	Register string `json:"register"` // casual | neutral | formal
	Instead  string `json:"instead"`  // the plainer thing you would have said
}

// Topic is tonight's 2 to 3 minute talk.
type Topic struct {
	Title       string   `json:"title"`
	Why         string   `json:"why"`         // why this topic grows you
	Angles      []string `json:"angles"`      // ways in, pick one
	Expressions []string `json:"expressions"` // useful phrases for this talk
	Structure   []string `json:"structure"`   // open, middle, close
	Twist       string   `json:"twist"`       // a question to answer at the end
}

// WordNote is a word in the passage worth noticing.
type WordNote struct {
	Word    string `json:"word"`
	Meaning string `json:"meaning"`
}

// Passage is the read-aloud text.
type Passage struct {
	Title     string     `json:"title"`
	Theme     string     `json:"theme"`
	Text      string     `json:"text"` // paragraphs separated by blank lines
	Words     []WordNote `json:"words"`
	Questions []string   `json:"questions"` // reflection questions
	Tips      []string   `json:"tips"`      // delivery tips: pauses, stress, pace
	WordCount int        `json:"wordCount"`
	Minutes   int        `json:"minutes"` // read-aloud estimate at ~140 words a minute
}

// Paragraphs splits the passage text on blank lines (for templates).
func (p Passage) Paragraphs() []string {
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(p.Text, "\r", ""), "\n\n") {
		if para = strings.TrimSpace(para); para != "" {
			out = append(out, para)
		}
	}
	return out
}

// Pack is one day's material plus its state.
type Pack struct {
	Day         string    `json:"day"`
	Phrases     []Phrase  `json:"phrases"`
	Topic       Topic     `json:"topic"`
	Passage     Passage   `json:"passage"`
	Model       string    `json:"model"`
	Status      string    `json:"status"` // ready | error
	Error       string    `json:"error,omitempty"`
	GeneratedAt time.Time `json:"generatedAt"`
	ReadDone    bool      `json:"readDone"`
	SpokeDone   bool      `json:"spokeDone"`
	Note        string    `json:"note,omitempty"`
}

// Prefs steer generation. Edited on the dashboard, stored in kv english_prefs.
type Prefs struct {
	Themes       []string `json:"themes"`       // passage themes to rotate through
	Focus        string   `json:"focus"`        // free text: what you want to get better at right now
	PhraseCount  int      `json:"phraseCount"`  // phrases per day
	PassageWords int      `json:"passageWords"` // target passage length
	Avoid        string   `json:"avoid"`        // topics to keep out
}

// Summary is one archive row.
type Summary struct {
	Day        string `json:"day"`
	Passage    string `json:"passage"`
	Topic      string `json:"topic"`
	Status     string `json:"status"`
	ReadDone   bool   `json:"readDone"`
	SpokeDone  bool   `json:"spokeDone"`
	PhraseList string `json:"-"`
}

// Status is what the Kindle snapshot carries under practice.english.
type Status struct {
	Today     *Pack  `json:"today,omitempty"`
	Generator string `json:"generator"` // live | not configured
	Days7     int    `json:"days7"`     // days in the last 7 with read or speak done
}

func DefaultPrefs() Prefs {
	return Prefs{
		Themes: []string{
			"personality development and habits",
			"storytelling: a true story about a person who changed course",
			"psychology of communication and listening",
			"science or nature explained like a story",
			"history: a small moment with a big effect",
			"work, craft and mastery",
			"travel and cultures",
			"money, decisions and trade-offs",
			"technology and how it changes daily life",
			"philosophy for everyday life",
		},
		Focus:        "Clear, confident speech in meetings and small talk. Fewer filler words. Better transitions between ideas.",
		PhraseCount:  8,
		PassageWords: 700,
		Avoid:        "politics, religion, war, celebrity gossip, death, disasters",
	}
}

/* ---------- service ---------- */

type Service struct {
	pool *pgxpool.Pool
	llm  *llm.Client
	tz   *time.Location
	mu   sync.Mutex
}

func New(pool *pgxpool.Pool, client *llm.Client, tz *time.Location) *Service {
	return &Service{pool: pool, llm: client, tz: tz}
}

func (s *Service) Configured() bool { return s.llm.Configured() }

func (s *Service) Today() time.Time {
	n := time.Now().In(s.tz)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, s.tz)
}

func (s *Service) Prefs(ctx context.Context) Prefs {
	p := DefaultPrefs()
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT value FROM kv WHERE key='english_prefs'`).Scan(&raw); err == nil {
		_ = json.Unmarshal(raw, &p)
	}
	if p.PhraseCount < 3 || p.PhraseCount > 15 {
		p.PhraseCount = 8
	}
	if p.PassageWords < 300 || p.PassageWords > 1200 {
		p.PassageWords = 700
	}
	if len(p.Themes) == 0 {
		p.Themes = DefaultPrefs().Themes
	}
	return p
}

func (s *Service) SavePrefs(ctx context.Context, p Prefs) error {
	clean := p.Themes[:0]
	for _, t := range p.Themes {
		if t = strings.TrimSpace(t); t != "" {
			clean = append(clean, t)
		}
	}
	p.Themes = clean
	if len(p.Themes) == 0 {
		return errors.New("add at least one theme")
	}
	if p.PhraseCount < 3 || p.PhraseCount > 15 {
		return errors.New("phrases per day must be 3 to 15")
	}
	if p.PassageWords < 300 || p.PassageWords > 1200 {
		return errors.New("passage length must be 300 to 1200 words")
	}
	raw, _ := json.Marshal(p)
	_, err := s.pool.Exec(ctx, `INSERT INTO kv(key,value) VALUES('english_prefs',$1) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, raw)
	return err
}

/* ---------- reads ---------- */

func (s *Service) Get(ctx context.Context, day time.Time) (*Pack, error) {
	var raw []byte
	p := &Pack{}
	err := s.pool.QueryRow(ctx, `SELECT day::text, pack, model, status, error, generated_at, read_done, spoke_done, note FROM english_packs WHERE day=$1`, day).
		Scan(&p.Day, &raw, &p.Model, &p.Status, &p.Error, &p.GeneratedAt, &p.ReadDone, &p.SpokeDone, &p.Note)
	if err != nil {
		return nil, err
	}
	var body struct {
		Phrases []Phrase `json:"phrases"`
		Topic   Topic    `json:"topic"`
		Passage Passage  `json:"passage"`
	}
	_ = json.Unmarshal(raw, &body)
	p.Phrases, p.Topic, p.Passage = body.Phrases, body.Topic, body.Passage
	if p.Phrases == nil {
		p.Phrases = []Phrase{}
	}
	return p, nil
}

// Recent lists the newest n packs (archive).
func (s *Service) Recent(ctx context.Context, n int) ([]Summary, error) {
	rows, err := s.pool.Query(ctx, `SELECT day::text, pack->'passage'->>'title', pack->'topic'->>'title', status, read_done, spoke_done,
		(SELECT string_agg(x->>'phrase', '; ') FROM jsonb_array_elements(coalesce(pack->'phrases','[]')) x)
		FROM english_packs ORDER BY day DESC LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var r Summary
		var pt, tt, ph *string
		if err := rows.Scan(&r.Day, &pt, &tt, &r.Status, &r.ReadDone, &r.SpokeDone, &ph); err != nil {
			return nil, err
		}
		if pt != nil {
			r.Passage = *pt
		}
		if tt != nil {
			r.Topic = *tt
		}
		if ph != nil {
			r.PhraseList = *ph
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StatusFor builds the Kindle view for a day.
func (s *Service) StatusFor(ctx context.Context, day time.Time) *Status {
	st := &Status{Generator: "not configured"}
	if s.Configured() {
		st.Generator = "live"
	}
	if p, err := s.Get(ctx, day); err == nil {
		st.Today = p
	}
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM english_packs WHERE day > $1::date - 7 AND day <= $1 AND (read_done OR spoke_done)`, day).Scan(&st.Days7)
	return st
}

/* ---------- writes ---------- */

// SetDone marks the read-aloud or the speaking part done for a day.
func (s *Service) SetDone(ctx context.Context, day time.Time, part string, done bool) error {
	var col string
	switch part {
	case "read":
		col = "read_done"
	case "speak":
		col = "spoke_done"
	default:
		return errors.New(`part must be "read" or "speak"`)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE english_packs SET `+col+`=$2 WHERE day=$1`, day, done)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("no pack for that day")
	}
	return nil
}

func (s *Service) SetNote(ctx context.Context, day time.Time, note string) error {
	_, err := s.pool.Exec(ctx, `UPDATE english_packs SET note=$2 WHERE day=$1`, day, strings.TrimSpace(note))
	return err
}

/* ---------- generation ---------- */

const systemPrompt = `You are an English communication coach for an adult professional from India (Hindi and Marathi speaker, fluent in English, works in software at a non-profit). He wants to articulate better: richer phrasing, fewer filler words, clear structure when he speaks, and confident delivery. You prepare material that is interesting on its own, builds personality and storytelling skill, and is pitched at a fluent speaker (not a beginner). Use natural, modern English. Avoid cliches, motivational fluff, and anything preachy. You answer only with the JSON that is asked for, no prose before or after.`

// Generate builds and stores the pack for a day. Existing material is replaced.
func (s *Service) Generate(ctx context.Context, day time.Time) (*Pack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Configured() {
		return nil, errors.New("ANTHROPIC_API_KEY is not set")
	}
	prefs := s.Prefs(ctx)
	recent, _ := s.Recent(ctx, 30)

	// rotate themes so consecutive days differ; the model may still pick another from the list
	theme := prefs.Themes[day.YearDay()%len(prefs.Themes)]

	var sb strings.Builder
	fmt.Fprintf(&sb, "ENGLISH DAILY PACK for %s.\n\n", day.Format("Monday 2 January 2006"))
	fmt.Fprintf(&sb, "LEARNER'S CURRENT FOCUS: %s\n", prefs.Focus)
	fmt.Fprintf(&sb, "KEEP OUT: %s\n", prefs.Avoid)
	fmt.Fprintf(&sb, "SUGGESTED THEME FOR TODAY'S PASSAGE: %s\n(other allowed themes: %s)\n", theme, strings.Join(prefs.Themes, "; "))
	if len(recent) > 0 {
		sb.WriteString("\nALREADY USED IN THE LAST 30 DAYS (do not repeat these titles, stories, topics or phrases; pick fresh ground):\n")
		for _, r := range recent {
			if r.Passage != "" || r.Topic != "" {
				fmt.Fprintf(&sb, "  %s: passage %q, topic %q\n", r.Day, r.Passage, r.Topic)
			}
			if r.PhraseList != "" {
				fmt.Fprintf(&sb, "    phrases: %s\n", r.PhraseList)
			}
		}
	}
	fmt.Fprintf(&sb, `
BUILD THREE PARTS.

1. PHRASES: %d expressions a fluent speaker uses in day-to-day talk at work and with friends. Mix registers (casual, neutral, formal). Each one should replace something plainer the learner would say now. Prefer phrases that help with: opening a point, disagreeing politely, buying time, summarising, telling a story, showing enthusiasm, closing a conversation. No idioms that sound dated or forced.

2. SPEAKING TOPIC: one topic to talk about alone for 2 to 3 minutes before bed. It should make him think, be personal enough to have real content, and build storytelling or self-reflection. Give 3 angles to pick from, 6 expressions useful for this talk, a 3 part structure (open, middle, close) in one line each, and a twist question to answer at the end.

3. READ-ALOUD PASSAGE: about %d words (that is 5 minutes aloud), written to be read aloud: varied sentence length, concrete images, one clear thread, a satisfying ending. Make it interesting and useful for personality development or storytelling (a real-feeling story, an idea explained through a person, a moment in history, a piece of science told as narrative). True facts only; if you use a real person, stick to well known, verifiable facts. Use blank lines between paragraphs. Then list 6 words or phrases from the text worth noticing with short meanings, 2 reflection questions, and 3 short delivery tips for this specific text (where to pause, which words to stress, where to slow down).

Return ONLY this JSON:
{"phrases":[{"phrase":"...","meaning":"...","example":"...","register":"casual|neutral|formal","instead":"the plainer version"}],
 "topic":{"title":"...","why":"...","angles":["...","...","..."],"expressions":["..."],"structure":["open: ...","middle: ...","close: ..."],"twist":"..."},
 "passage":{"title":"...","theme":"...","text":"...","words":[{"word":"...","meaning":"..."}],"questions":["...","..."],"tips":["...","...","..."]}}`, prefs.PhraseCount, prefs.PassageWords)

	var out struct {
		Phrases []Phrase `json:"phrases"`
		Topic   Topic    `json:"topic"`
		Passage Passage  `json:"passage"`
	}
	usage, err := s.llm.JSON(ctx, systemPrompt, sb.String(), 8000, &out)
	p := &Pack{Day: day.Format("2006-01-02"), GeneratedAt: time.Now(), Status: "ready", Model: s.llm.Model, Phrases: []Phrase{}}
	if err == nil {
		err = validate(out.Phrases, out.Topic, out.Passage)
	}
	if err != nil {
		p.Status, p.Error = "error", err.Error()
		log.Printf("english: pack %s: %v", p.Day, err)
	} else {
		out.Passage.WordCount = len(strings.Fields(out.Passage.Text))
		out.Passage.Minutes = (out.Passage.WordCount + 70) / 140
		p.Phrases, p.Topic, p.Passage = out.Phrases, out.Topic, out.Passage
		log.Printf("english: pack %s generated (%d phrases, %d word passage, %d in / %d out tokens)", p.Day, len(p.Phrases), p.Passage.WordCount, usage.InputTokens, usage.OutputTokens)
	}
	raw, _ := json.Marshal(map[string]any{"phrases": p.Phrases, "topic": p.Topic, "passage": p.Passage})
	if _, dbErr := s.pool.Exec(ctx, `INSERT INTO english_packs(day,pack,model,status,error,generated_at) VALUES($1,$2,$3,$4,$5,now())
		ON CONFLICT (day) DO UPDATE SET pack=EXCLUDED.pack, model=EXCLUDED.model, status=EXCLUDED.status, error=EXCLUDED.error, generated_at=now()`,
		day, raw, p.Model, p.Status, p.Error); dbErr != nil {
		return nil, dbErr
	}
	if err != nil {
		return p, err
	}
	return p, nil
}

func validate(ph []Phrase, t Topic, pa Passage) error {
	if len(ph) < 3 {
		return fmt.Errorf("model returned %d phrases, want at least 3", len(ph))
	}
	for i, p := range ph {
		if strings.TrimSpace(p.Phrase) == "" || strings.TrimSpace(p.Meaning) == "" {
			return fmt.Errorf("phrase %d is incomplete", i+1)
		}
	}
	if strings.TrimSpace(t.Title) == "" || len(t.Angles) == 0 {
		return errors.New("speaking topic is incomplete")
	}
	if words := len(strings.Fields(pa.Text)); words < 250 {
		return fmt.Errorf("passage has only %d words", words)
	}
	if strings.TrimSpace(pa.Title) == "" {
		return errors.New("passage has no title")
	}
	return nil
}

/* ---------- scheduler ---------- */

// EnsureToday generates today's pack if missing or failed, and from 18:00 tomorrow's too.
// Safe to call often; it never regenerates a ready pack.
func (s *Service) EnsureToday(ctx context.Context) {
	if !s.Configured() {
		return
	}
	now := time.Now().In(s.tz)
	need := []time.Time{s.Today()}
	if now.Hour() >= 18 {
		need = append(need, s.Today().AddDate(0, 0, 1))
	}
	for _, d := range need {
		if p, err := s.Get(ctx, d); err == nil && p.Status == "ready" {
			continue
		}
		if _, err := s.Generate(ctx, d); err != nil {
			log.Printf("english: ensure %s: %v", d.Format("2006-01-02"), err)
		}
	}
}
