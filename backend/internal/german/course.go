// Package german runs the A1 to B2 course: day and week arithmetic, quiz and test generation
// through the Claude API, marking, results and the error log.
package german

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

type Week struct {
	Week       int    `json:"week"`
	Phase      string `json:"phase"`
	Grammar    string `json:"grammar"`
	Vocab      string `json:"vocab"`
	Phrases    string `json:"phrases,omitempty"`
	Speaking   string `json:"speaking"`
	Saturday   string `json:"saturday"`
	Checkpoint string `json:"checkpoint,omitempty"`
}

type Settings struct {
	StartDate  string            `json:"start_date"`
	ExamTarget string            `json:"exam_target"`
	QuizDesign map[string]string `json:"quiz_design"`
	Written    map[string]string `json:"written_design"`
	Statements []string          `json:"statements"`
}

// Question is one multiple choice item. Answer is the index into Options.
type Question struct {
	Type        string   `json:"type"`
	Prompt      string   `json:"prompt"`
	Options     []string `json:"options"`
	Answer      int      `json:"answer"`
	Explanation string   `json:"explanation"`
}

type Quiz struct {
	Day         int        `json:"day"`
	Week        int        `json:"week"`
	Kind        string     `json:"kind"` // daily | friday
	Questions   []Question `json:"questions"`
	GeneratedAt time.Time  `json:"generatedAt"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
}

// WrittenTask is the Saturday test: an optional reading part with multiple choice items
// (gradable on the Kindle) and a writing part answered on paper, then typed into the dashboard.
type WrittenTask struct {
	Title        string     `json:"title"`
	Level        string     `json:"level"`
	TimeMinutes  int        `json:"timeMinutes"`
	ReadingText  string     `json:"readingText,omitempty"`
	ReadingItems []Question `json:"readingItems,omitempty"`
	WritingTask  string     `json:"writingTask"`
	Points       []string   `json:"points"`
	WordCount    string     `json:"wordCount"`
	Criteria     []string   `json:"criteria"`
}

type Marking struct {
	Score       int       `json:"score"` // 0 to 100
	Criteria    []CritRow `json:"criteria"`
	Errors      []ErrRow  `json:"errors"`
	ModelAnswer string    `json:"modelAnswer"`
	Summary     string    `json:"summary"`
}

type CritRow struct {
	Name    string `json:"name"`
	Points  int    `json:"points"`
	Max     int    `json:"max"`
	Comment string `json:"comment"`
}

type ErrRow struct {
	Wrong   string `json:"wrong"`
	Correct string `json:"correct"`
	Rule    string `json:"rule"`
}

type Written struct {
	Week         int          `json:"week"`
	Task         WrittenTask  `json:"task"`
	Status       string       `json:"status"`
	Error        string       `json:"error,omitempty"`
	GeneratedAt  time.Time    `json:"generatedAt"`
	Submission   string       `json:"submission,omitempty"`
	ReadingScore *int         `json:"readingScore,omitempty"`
	SubmittedAt  *time.Time   `json:"submittedAt,omitempty"`
	Marking      *Marking     `json:"marking,omitempty"`
	MarkedAt     *time.Time   `json:"markedAt,omitempty"`
}

type Result struct {
	Day        int       `json:"day"`
	AnsweredAt time.Time `json:"answeredAt"`
	Score      int       `json:"score"`
	Total      int       `json:"total"`
}

// Status is what the Kindle and the dashboard see for today.
type Status struct {
	Started     bool      `json:"started"`
	Day         int       `json:"day"`   // 1..365, 0 before the start
	Week        int       `json:"week"`  // 1..53
	Weekday     string    `json:"weekday"`
	Phase       string    `json:"phase"`
	DaysToExam  int       `json:"daysToExam"`
	Plan        *Week     `json:"plan,omitempty"`
	Quiz        *Quiz     `json:"quiz,omitempty"`        // today's quiz, if generated (Mon to Fri)
	TodayResult *Result   `json:"todayResult,omitempty"` // if already answered
	Written     *Written  `json:"written,omitempty"`     // this week's Saturday test
	Recent      []Result  `json:"recent"`                // last 7 quiz results
	ErrorCount  int       `json:"errorCount"`
	Generator   string    `json:"generator"` // live | not configured
}

type Course struct {
	pool *pgxpool.Pool
	llm  *llm.Client
	tz   *time.Location
	startOverride string
	mu   sync.Mutex
}

func New(pool *pgxpool.Pool, client *llm.Client, tz *time.Location, startOverride string) *Course {
	return &Course{pool: pool, llm: client, tz: tz, startOverride: startOverride}
}

func (c *Course) Configured() bool { return c.llm.Configured() }

/* ---------- plan access ---------- */

func (c *Course) Settings(ctx context.Context) (Settings, error) {
	var raw []byte
	var s Settings
	if err := c.pool.QueryRow(ctx, `SELECT value FROM kv WHERE key='german_course'`).Scan(&raw); err != nil {
		return s, errors.New("course not loaded: run `dashboard import` (data/german/course.yaml)")
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	if c.startOverride != "" {
		s.StartDate = c.startOverride
	}
	return s, nil
}

func (c *Course) startDate(ctx context.Context) (time.Time, error) {
	s, err := c.Settings(ctx)
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.ParseInLocation("2006-01-02", s.StartDate, c.tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("course start_date: %w", err)
	}
	return t, nil
}

func (c *Course) today() time.Time {
	n := time.Now().In(c.tz)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, c.tz)
}

// DayFor returns the course day number for a date (0 before the start).
func (c *Course) DayFor(ctx context.Context, date time.Time) (int, error) {
	start, err := c.startDate(ctx)
	if err != nil {
		return 0, err
	}
	d := int(date.Sub(start).Hours()/24+0.5) + 1
	if d < 0 {
		d = 0
	}
	return d, nil
}

func WeekOf(day int) int {
	if day <= 0 {
		return 0
	}
	return (day-1)/7 + 1
}

func (c *Course) GetWeek(ctx context.Context, week int) (*Week, error) {
	var w Week
	err := c.pool.QueryRow(ctx, `SELECT week,phase,grammar,vocab,phrases,speaking,saturday,checkpoint FROM german_weeks WHERE week=$1`, week).
		Scan(&w.Week, &w.Phase, &w.Grammar, &w.Vocab, &w.Phrases, &w.Speaking, &w.Saturday, &w.Checkpoint)
	if err != nil {
		return nil, fmt.Errorf("week %d is not in the plan", week)
	}
	return &w, nil
}

func (c *Course) Weeks(ctx context.Context) ([]Week, error) {
	rows, err := c.pool.Query(ctx, `SELECT week,phase,grammar,vocab,phrases,speaking,saturday,checkpoint FROM german_weeks ORDER BY week`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Week
	for rows.Next() {
		var w Week
		if err := rows.Scan(&w.Week, &w.Phase, &w.Grammar, &w.Vocab, &w.Phrases, &w.Speaking, &w.Saturday, &w.Checkpoint); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (c *Course) SaveWeek(ctx context.Context, w Week) error {
	_, err := c.pool.Exec(ctx, `INSERT INTO german_weeks(week,phase,grammar,vocab,phrases,speaking,saturday,checkpoint) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (week) DO UPDATE SET phase=EXCLUDED.phase, grammar=EXCLUDED.grammar, vocab=EXCLUDED.vocab, phrases=EXCLUDED.phrases,
		speaking=EXCLUDED.speaking, saturday=EXCLUDED.saturday, checkpoint=EXCLUDED.checkpoint`,
		w.Week, w.Phase, w.Grammar, w.Vocab, w.Phrases, w.Speaking, w.Saturday, w.Checkpoint)
	return err
}

/* ---------- status ---------- */

func (c *Course) StatusFor(ctx context.Context, date time.Time) (*Status, error) {
	st := &Status{Recent: []Result{}, Generator: "not configured"}
	if c.Configured() {
		st.Generator = "live"
	}
	day, err := c.DayFor(ctx, date)
	if err != nil {
		return st, err
	}
	st.Day, st.Week, st.Weekday = day, WeekOf(day), date.Weekday().String()
	st.Started = day >= 1
	start, _ := c.startDate(ctx)
	st.DaysToExam = int(start.AddDate(0, 0, 364).Sub(date).Hours() / 24)
	if !st.Started {
		return st, nil
	}
	if w, err := c.GetWeek(ctx, st.Week); err == nil {
		st.Plan = w
		st.Phase = w.Phase
	}
	if date.Weekday() != time.Saturday && date.Weekday() != time.Sunday {
		st.Quiz, _ = c.GetQuiz(ctx, day)
		st.TodayResult, _ = c.resultFor(ctx, day)
	}
	st.Written, _ = c.GetWritten(ctx, st.Week)
	st.Recent, _ = c.RecentResults(ctx, 7)
	_ = c.pool.QueryRow(ctx, `SELECT count(*) FROM german_errors`).Scan(&st.ErrorCount)
	return st, nil
}

/* ---------- quizzes ---------- */

func (c *Course) GetQuiz(ctx context.Context, day int) (*Quiz, error) {
	var q Quiz
	var raw []byte
	err := c.pool.QueryRow(ctx, `SELECT day,week,kind,questions,status,error,generated_at FROM german_quizzes WHERE day=$1`, day).
		Scan(&q.Day, &q.Week, &q.Kind, &raw, &q.Status, &q.Error, &q.GeneratedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(raw, &q.Questions)
	if q.Questions == nil {
		q.Questions = []Question{}
	}
	return &q, nil
}

func (c *Course) resultFor(ctx context.Context, day int) (*Result, error) {
	var r Result
	err := c.pool.QueryRow(ctx, `SELECT day,answered_at,score,total FROM german_quiz_results WHERE day=$1 ORDER BY id DESC LIMIT 1`, day).Scan(&r.Day, &r.AnsweredAt, &r.Score, &r.Total)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Course) RecentResults(ctx context.Context, n int) ([]Result, error) {
	rows, err := c.pool.Query(ctx, `SELECT DISTINCT ON (day) day,answered_at,score,total FROM german_quiz_results ORDER BY day DESC, id DESC LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Result{}
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.Day, &r.AnsweredAt, &r.Score, &r.Total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (c *Course) errorLog(ctx context.Context, limit int) ([]ErrRow, error) {
	rows, err := c.pool.Query(ctx, `SELECT wrong, correct, rule FROM german_errors ORDER BY count DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ErrRow
	for rows.Next() {
		var e ErrRow
		if err := rows.Scan(&e.Wrong, &e.Correct, &e.Rule); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (c *Course) ErrorLog(ctx context.Context, limit int) ([]ErrRow, error) { return c.errorLog(ctx, limit) }

func (c *Course) AddError(ctx context.Context, day int, e ErrRow) error {
	if strings.TrimSpace(e.Wrong) == "" || strings.TrimSpace(e.Correct) == "" {
		return nil
	}
	_, err := c.pool.Exec(ctx, `INSERT INTO german_errors(day,wrong,correct,rule) VALUES($1,$2,$3,$4)
		ON CONFLICT (wrong,correct) DO UPDATE SET count=german_errors.count+1, rule=CASE WHEN EXCLUDED.rule<>'' THEN EXCLUDED.rule ELSE german_errors.rule END`,
		day, strings.TrimSpace(e.Wrong), strings.TrimSpace(e.Correct), strings.TrimSpace(e.Rule))
	return err
}

func (c *Course) DeleteError(ctx context.Context, id int) error {
	_, err := c.pool.Exec(ctx, `DELETE FROM german_errors WHERE id=$1`, id)
	return err
}

const systemPrompt = `You are a German teacher preparing a learner for the Goethe-Zertifikat B2. The learner is an adult from India (Hindi and Marathi speaker, fluent in English) following a 365 day self-study plan from A1 to B2. You write exam-style practice material that is accurate, natural German, pitched exactly at the learner's current week. Instructions and explanations are in simple English; German examples are correct in spelling, articles, cases and word order. You answer only with the JSON that is asked for, no prose before or after.`

// GenerateQuiz builds and stores the quiz for a course day (Mon to Fri). Friday is cumulative.
func (c *Course) GenerateQuiz(ctx context.Context, day int) (*Quiz, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.Configured() {
		return nil, errors.New("ANTHROPIC_API_KEY is not set")
	}
	week := WeekOf(day)
	w, err := c.GetWeek(ctx, week)
	if err != nil {
		return nil, err
	}
	s, err := c.Settings(ctx)
	if err != nil {
		return nil, err
	}
	start, _ := c.startDate(ctx)
	date := start.AddDate(0, 0, day-1)
	kind := "daily"
	if date.Weekday() == time.Friday {
		kind = "friday"
	}
	dayInWeek := (day-1)%7 + 1

	var sb strings.Builder
	fmt.Fprintf(&sb, "Course day %d of 365 (%s), week %d, phase %s, day %d of the week.\n\n", day, date.Format("Monday 2 January 2006"), week, w.Phase, dayInWeek)
	fmt.Fprintf(&sb, "THIS WEEK'S PLAN\nGrammar focus: %s\nVocabulary theme: %s\n", w.Grammar, w.Vocab)
	if w.Phrases != "" {
		fmt.Fprintf(&sb, "Phrase bank function: %s\n", w.Phrases)
	}
	if kind == "friday" {
		sb.WriteString("\nThis is the FRIDAY quiz: cover the whole week's grammar and vocabulary, and include 3 questions that repeat grammar from EARLIER weeks listed below.\n")
		rows, _ := c.pool.Query(ctx, `SELECT week, grammar FROM german_weeks WHERE week < $1 ORDER BY week`, week)
		if rows != nil {
			for rows.Next() {
				var wk int
				var g string
				if rows.Scan(&wk, &g) == nil {
					fmt.Fprintf(&sb, "  Week %d: %s\n", wk, g)
				}
			}
			rows.Close()
		}
	} else {
		fmt.Fprintf(&sb, "\nThis is a weekday quiz on day %d of the week: concentrate on the part of the grammar focus a learner would have covered by now, plus this week's vocabulary. Mix in 2 questions from last week if there was one.\n", dayInWeek)
	}
	design := s.QuizDesign[w.Phase]
	if design == "" {
		design = s.QuizDesign["B1"]
	}
	fmt.Fprintf(&sb, "\nQUESTION MIX FOR THIS LEVEL (%s): %s\n", w.Phase, design)
	if w.Phase == "B2" || w.Phase == "Prep" {
		if errs, _ := c.errorLog(ctx, 12); len(errs) > 0 {
			sb.WriteString("\nLEARNER'S ERROR LOG (build questions that test exactly these mistakes, reworded):\n")
			for _, e := range errs {
				fmt.Fprintf(&sb, "  wrong: %s | correct: %s | rule: %s\n", e.Wrong, e.Correct, e.Rule)
			}
		}
	}
	sb.WriteString(`
FORMAT. Every question is multiple choice with exactly 4 options and one correct option, because it is answered by tapping on an e-ink Kindle. Sentence-order questions give 4 candidate orderings. Listening-style questions give a short line and 4 possible replies. Keep prompts under 160 characters and options under 60 characters. Use "___" for gaps. Write 14 questions.

Return ONLY this JSON:
{"questions":[{"type":"vocabulary|grammar|order|reply|reading|register|phrase|error|transform","prompt":"...","options":["...","...","...","..."],"answer":0,"explanation":"one or two sentences in simple English, mention the rule"}]}
"answer" is the zero-based index of the correct option. Vary the position of the correct answer.`)

	var out struct {
		Questions []Question `json:"questions"`
	}
	usage, err := c.llm.JSON(ctx, systemPrompt, sb.String(), 6000, &out)
	q := &Quiz{Day: day, Week: week, Kind: kind, GeneratedAt: time.Now(), Status: "ready"}
	if err == nil {
		err = validateQuestions(out.Questions)
	}
	if err != nil {
		q.Status, q.Error, q.Questions = "error", err.Error(), []Question{}
		log.Printf("german: quiz day %d: %v", day, err)
	} else {
		q.Questions = out.Questions
		log.Printf("german: quiz day %d generated (%d questions, %d in / %d out tokens)", day, len(q.Questions), usage.InputTokens, usage.OutputTokens)
	}
	raw, _ := json.Marshal(q.Questions)
	if _, dbErr := c.pool.Exec(ctx, `INSERT INTO german_quizzes(day,week,kind,questions,model,status,error,generated_at) VALUES($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT (day) DO UPDATE SET week=EXCLUDED.week, kind=EXCLUDED.kind, questions=EXCLUDED.questions, model=EXCLUDED.model, status=EXCLUDED.status, error=EXCLUDED.error, generated_at=now()`,
		day, week, kind, raw, c.llm.Model, q.Status, q.Error); dbErr != nil {
		return nil, dbErr
	}
	if err != nil {
		return q, err
	}
	return q, nil
}

func validateQuestions(qs []Question) error {
	if len(qs) < 8 {
		return fmt.Errorf("model returned %d questions, expected at least 8", len(qs))
	}
	for i, q := range qs {
		if strings.TrimSpace(q.Prompt) == "" || len(q.Options) < 3 || len(q.Options) > 5 {
			return fmt.Errorf("question %d is malformed", i+1)
		}
		if q.Answer < 0 || q.Answer >= len(q.Options) {
			return fmt.Errorf("question %d has answer index out of range", i+1)
		}
	}
	return nil
}

// RecordResult stores a quiz result and feeds wrong answers into the error log.
func (c *Course) RecordResult(ctx context.Context, day int, answers []int) (*Result, error) {
	q, err := c.GetQuiz(ctx, day)
	if err != nil {
		return nil, errors.New("no quiz for that day")
	}
	score := 0
	for i, qq := range q.Questions {
		if i < len(answers) && answers[i] == qq.Answer {
			score++
		} else if i < len(answers) && answers[i] >= 0 && answers[i] < len(qq.Options) {
			_ = c.AddError(ctx, day, ErrRow{Wrong: qq.Prompt + " -> " + qq.Options[answers[i]], Correct: qq.Options[qq.Answer], Rule: qq.Explanation})
		}
	}
	raw, _ := json.Marshal(answers)
	r := &Result{Day: day, AnsweredAt: time.Now(), Score: score, Total: len(q.Questions)}
	if _, err := c.pool.Exec(ctx, `INSERT INTO german_quiz_results(day,score,total,answers) VALUES($1,$2,$3,$4)`, day, score, len(q.Questions), raw); err != nil {
		return nil, err
	}
	return r, nil
}

/* ---------- Saturday written test ---------- */

func (c *Course) GetWritten(ctx context.Context, week int) (*Written, error) {
	var w Written
	var task, marking []byte
	err := c.pool.QueryRow(ctx, `SELECT week,task,status,error,generated_at,submission,reading_score,submitted_at,marking,marked_at FROM german_written WHERE week=$1`, week).
		Scan(&w.Week, &task, &w.Status, &w.Error, &w.GeneratedAt, &w.Submission, &w.ReadingScore, &w.SubmittedAt, &marking, &w.MarkedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(task, &w.Task)
	if marking != nil {
		var m Marking
		if json.Unmarshal(marking, &m) == nil {
			w.Marking = &m
		}
	}
	return &w, nil
}

func (c *Course) GenerateWritten(ctx context.Context, week int) (*Written, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.Configured() {
		return nil, errors.New("ANTHROPIC_API_KEY is not set")
	}
	w, err := c.GetWeek(ctx, week)
	if err != nil {
		return nil, err
	}
	s, err := c.Settings(ctx)
	if err != nil {
		return nil, err
	}
	design := s.Written[w.Phase]
	var sb strings.Builder
	fmt.Fprintf(&sb, "Build the SATURDAY WRITTEN TEST for week %d, phase %s.\n\nThis week's plan:\nGrammar: %s\nVocabulary: %s\n", week, w.Phase, w.Grammar, w.Vocab)
	if w.Phrases != "" {
		fmt.Fprintf(&sb, "Phrase bank: %s\n", w.Phrases)
	}
	fmt.Fprintf(&sb, "The plan says this Saturday's test is: %s\n\nTest design for this level: %s\n", w.Saturday, design)
	if w.Phase == "B2" || w.Phase == "Prep" {
		fmt.Fprintf(&sb, "\nGoethe B2 discussion statements you may draw a topic from: %s\n", strings.Join(s.Statements, " | "))
	}
	sb.WriteString(`
Produce one test with: (1) a reading part when the plan calls for reading or the level design includes one: a short German text at the level (notice, email, adverts, forum post or article, 60 to 220 words depending on level) with 5 multiple choice items (4 options, one correct, answered by tapping on a Kindle); if the plan's Saturday task is writing only, leave readingText empty and readingItems as []; (2) a writing task matching the plan, with the exact points the learner must cover, a word count, and the marking criteria the examiner will use.

Return ONLY this JSON:
{"title":"...","level":"A1|A2|B1|B2","timeMinutes":30,"readingText":"...","readingItems":[{"type":"reading","prompt":"...","options":["...","...","...","..."],"answer":0,"explanation":"..."}],"writingTask":"the full instructions in English with the German situation","points":["point 1","point 2","point 3"],"wordCount":"about 60 words","criteria":["Erfüllung: ...","Kohärenz: ...","Wortschatz: ...","Strukturen: ..."]}`)

	var task WrittenTask
	_, err = c.llm.JSON(ctx, systemPrompt, sb.String(), 4000, &task)
	status, errText := "ready", ""
	if err == nil && (strings.TrimSpace(task.WritingTask) == "" || len(task.Points) == 0) {
		err = errors.New("model returned no writing task")
	}
	if err == nil && len(task.ReadingItems) > 0 {
		err = validateQuestions(append(make([]Question, 0), padTo8(task.ReadingItems)...))
	}
	if err != nil {
		status, errText = "error", err.Error()
		log.Printf("german: written week %d: %v", week, err)
	}
	raw, _ := json.Marshal(task)
	if _, dbErr := c.pool.Exec(ctx, `INSERT INTO german_written(week,task,model,status,error,generated_at) VALUES($1,$2,$3,$4,$5,now())
		ON CONFLICT (week) DO UPDATE SET task=EXCLUDED.task, model=EXCLUDED.model, status=EXCLUDED.status, error=EXCLUDED.error, generated_at=now()`,
		week, raw, c.llm.Model, status, errText); dbErr != nil {
		return nil, dbErr
	}
	out, _ := c.GetWritten(ctx, week)
	return out, err
}

// padTo8 lets validateQuestions (min 8) check a 5-item reading part.
func padTo8(qs []Question) []Question {
	for len(qs) < 8 {
		qs = append(qs, qs[0])
	}
	return qs
}

// SetReadingScore stores the Kindle-graded reading part (out of the number of reading items).
func (c *Course) SetReadingScore(ctx context.Context, week, score int) error {
	tag, err := c.pool.Exec(ctx, `UPDATE german_written SET reading_score=$2 WHERE week=$1`, week, score)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("no written test for that week")
	}
	return nil
}

// Submit stores the typed answer (and the reading score if the reading part was done) and marks it.
func (c *Course) Submit(ctx context.Context, week int, submission string, readingScore *int) (*Written, error) {
	w, err := c.GetWritten(ctx, week)
	if err != nil {
		return nil, errors.New("no written test for that week")
	}
	submission = strings.TrimSpace(submission)
	if len(submission) < 20 {
		return nil, errors.New("the answer is too short to mark")
	}
	if _, err := c.pool.Exec(ctx, `UPDATE german_written SET submission=$2, reading_score=$3, submitted_at=now() WHERE week=$1`, week, submission, readingScore); err != nil {
		return nil, err
	}
	if !c.Configured() {
		return c.GetWritten(ctx, week)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var sb strings.Builder
	fmt.Fprintf(&sb, "Mark this learner's answer to a %s level writing task against the Goethe criteria. Be strict but fair, as a Goethe examiner.\n\nTASK: %s\nPoints to cover: %s\nWord count: %s\nCriteria: %s\n\nLEARNER'S ANSWER:\n%s\n", w.Task.Level, w.Task.WritingTask, strings.Join(w.Task.Points, "; "), w.Task.WordCount, strings.Join(w.Task.Criteria, "; "), submission)
	sb.WriteString(`
Return ONLY this JSON:
{"score":0,"criteria":[{"name":"Erfüllung","points":0,"max":25,"comment":"..."}],"errors":[{"wrong":"exact phrase from the answer","correct":"corrected phrase","rule":"the rule in one short sentence"}],"modelAnswer":"a model answer at this level, same length as asked","summary":"three sentences: the mark, the three biggest problems, what to practise this week"}
"score" is 0 to 100 (the criteria points scaled). List the 3 to 6 most important errors, biggest first.`)
	var m Marking
	if _, err := c.llm.JSON(ctx, systemPrompt, sb.String(), 4000, &m); err != nil {
		return nil, err
	}
	if m.Score < 0 {
		m.Score = 0
	}
	if m.Score > 100 {
		m.Score = 100
	}
	raw, _ := json.Marshal(m)
	if _, err := c.pool.Exec(ctx, `UPDATE german_written SET marking=$2, marked_at=now() WHERE week=$1`, week, raw); err != nil {
		return nil, err
	}
	for _, e := range m.Errors {
		_ = c.AddError(ctx, week*7, e)
	}
	return c.GetWritten(ctx, week)
}

/* ---------- scheduler ---------- */

// EnsureToday generates today's quiz (Mon to Fri) and this week's Saturday test if missing,
// and from 18:00 pre-generates tomorrow's quiz. Safe to call often.
func (c *Course) EnsureToday(ctx context.Context) {
	if !c.Configured() {
		return
	}
	now := time.Now().In(c.tz)
	today := c.today()
	day, err := c.DayFor(ctx, today)
	if err != nil || day < 1 || day > 371 {
		return
	}
	need := []int{}
	if wd := today.Weekday(); wd != time.Saturday && wd != time.Sunday {
		need = append(need, day)
	}
	if now.Hour() >= 18 {
		tomorrow := today.AddDate(0, 0, 1)
		if wd := tomorrow.Weekday(); wd != time.Saturday && wd != time.Sunday {
			need = append(need, day+1)
		}
	}
	for _, d := range need {
		if q, err := c.GetQuiz(ctx, d); err == nil && q.Status == "ready" {
			continue
		}
		if _, err := c.GenerateQuiz(ctx, d); err != nil {
			log.Printf("german: ensure quiz day %d: %v", d, err)
		}
	}
	week := WeekOf(day)
	if w, err := c.GetWritten(ctx, week); err != nil || w.Status != "ready" {
		if _, err := c.GenerateWritten(ctx, week); err != nil {
			log.Printf("german: ensure written week %d: %v", week, err)
		}
	}
}

// Run calls EnsureToday now and then every interval until ctx ends.
func (c *Course) Run(ctx context.Context, every time.Duration) {
	c.EnsureToday(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.EnsureToday(ctx)
		}
	}
}
