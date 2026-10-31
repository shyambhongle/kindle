// Package model holds the JSON shape the Kindle reads. Keep in sync with the KOReader plugin
// (koreader-plugin/dashboard.koplugin/dashboard_render.lua and tools/mock.lua).
package model

import (
	"encoding/json"
	"time"
)

type Dashboard struct {
	GeneratedAt time.Time         `json:"generatedAt"`
	Weather     Weather           `json:"weather"`
	Forecast    []PlaceForecast   `json:"forecast"` // home first, then the other places
	Events      []Event           `json:"events"`
	Tasks       []Task            `json:"tasks"`
	TaskCounts  TaskCounts        `json:"taskCounts"`
	TasksSynced time.Time         `json:"tasksSynced"`
	Practice    Practice          `json:"practice"`
	News        []News            `json:"news"`     // curated general feeds
	HN          []News            `json:"hn"`       // Hacker News, shown in its own column
	Security    []News            `json:"security"` // developer security feeds (Socket and friends)
	Sources     map[string]string `json:"sources"` // source name -> "live" | "mock" | error text
}

// Weather is today's weather at home, kept for the Kindle's top bar.
type Weather struct {
	TempC    float64 `json:"tempC"`
	HiC      float64 `json:"hiC"`
	LoC      float64 `json:"loC"`
	Summary  string  `json:"summary"`
	Location string  `json:"location"`
}

// Place is a location you follow: home plus up to four more, managed in the web dashboard.
type Place struct {
	ID       int     `json:"id,omitempty"`
	Name     string  `json:"name"`
	Label    string  `json:"label,omitempty"` // "Pune, Maharashtra, India"
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Timezone string  `json:"timezone,omitempty"`
	Home     bool    `json:"home"`
	Sort     int     `json:"-"`
}

type Current struct {
	TempC    float64 `json:"tempC"`
	Code     int     `json:"code"`
	Summary  string  `json:"summary"`
	Humidity int     `json:"humidity"`
	WindKmh  float64 `json:"windKmh"`
}

type ForecastDay struct {
	Date    string  `json:"date"` // YYYY-MM-DD in the place's local time
	HiC     float64 `json:"hiC"`
	LoC     float64 `json:"loC"`
	Code    int     `json:"code"`
	Summary string  `json:"summary"`
	RainPct int     `json:"rainPct"`
	Sunrise string  `json:"sunrise,omitempty"`
	Sunset  string  `json:"sunset,omitempty"`
}

type PlaceForecast struct {
	Place     Place         `json:"place"`
	Current   Current       `json:"current"`
	Days      []ForecastDay `json:"days"`
	FetchedAt time.Time     `json:"fetchedAt"`
	Error     string        `json:"error,omitempty"`
}

type Event struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Location string    `json:"location"`
}

// Task kinds, derived from the Notion status via the configured mapping.
const (
	KindTop     = "top"     // shown as Top 3
	KindNext    = "next"    // shown under the Top 3
	KindBacklog = "backlog" // counted, not sent to the Kindle
	KindHidden  = "hidden"  // ignored
	KindDone    = "done"    // finished today
)

type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Project     string `json:"project"`
	EstimateMin int    `json:"estimateMin"`
	Done        bool   `json:"done"`
	Top         bool   `json:"top"`
	NextStep    string `json:"nextStep"`
	Status      string `json:"status,omitempty"` // Notion status name
	Kind        string `json:"kind,omitempty"`
	Due         string `json:"due,omitempty"` // YYYY-MM-DD
	URL         string `json:"url,omitempty"`
	Sort        int    `json:"-"`
}

// TaskCounts summarises the whole board, including tasks not sent to the Kindle.
type TaskCounts struct {
	Top     int `json:"top"`
	Next    int `json:"next"`
	Backlog int `json:"backlog"`
	Done    int `json:"doneToday"`
}

type Practice struct {
	Guitar  Guitar  `json:"guitar"`
	German  German  `json:"german"`
	Vocab   Vocab   `json:"vocab"`
	Reading Reading `json:"reading"`
	English json.RawMessage `json:"english,omitempty"` // english.Status: today's phrases, speaking topic, read-aloud passage
}

type Guitar struct {
	DoneToday bool     `json:"doneToday"`
	Last7     []int    `json:"last7"`
	Week      int      `json:"week"`
	Current   string   `json:"current"`
	Today     string   `json:"today"`
	Plan      []string `json:"plan"`
	Notes     string   `json:"notes"`
}

type DEEN struct {
	DE string `json:"de"`
	EN string `json:"en"`
}

type German struct {
	DoneToday  bool            `json:"doneToday"`
	Last7      []int           `json:"last7"`
	Level      string          `json:"level"`
	DueReviews int             `json:"dueReviews"`
	Sentences  []DEEN          `json:"sentences"`
	Review     []DEEN          `json:"review"`
	Course     json.RawMessage `json:"course,omitempty"` // german.Status: day, week, plan, today's quiz, written test
}

type Word struct {
	Word       string `json:"word"`
	Pos        string `json:"pos,omitempty"`
	Definition string `json:"definition"`
	Example    string `json:"example,omitempty"`
}

type Vocab struct {
	DoneToday  bool   `json:"doneToday"`
	Last7      []int  `json:"last7"`
	DueReviews int    `json:"dueReviews"`
	Words      []Word `json:"words"`
	Review     []Word `json:"review"`
}

type Book struct {
	Title     string `json:"title"`
	Author    string `json:"author"`
	Page      int    `json:"page"`
	Pages     int    `json:"pages"`
	StartedOn string `json:"startedOn,omitempty"`
}

type Session struct {
	Date    string `json:"date"`
	Pages   int    `json:"pages"`
	Minutes int    `json:"minutes"`
}

type Reading struct {
	DoneToday bool      `json:"doneToday"`
	Last7     []int     `json:"last7"`
	Book      Book      `json:"book"`
	Sessions  []Session `json:"sessions"`
	Next      []Book    `json:"next"`
	// summaries.Status: plan day, today's book with its full summary, the next books.
	// Book above mirrors today's book (Page = plan day, Pages = books in the plan).
	Summary json.RawMessage `json:"summary,omitempty"`
}

type News struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	Link        string    `json:"link,omitempty"`
	Category    string    `json:"category,omitempty"` // general | hn | security
	PublishedAt time.Time `json:"publishedAt"`
	Alert       bool      `json:"alert"` // security item that looks like a real compromise
	FeedURL     string    `json:"-"`
}
