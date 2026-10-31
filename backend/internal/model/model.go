// Package model holds the JSON shape the Kindle client reads. Keep in sync with client/index.html.
package model

import "time"

type Dashboard struct {
	GeneratedAt time.Time         `json:"generatedAt"`
	Weather     Weather           `json:"weather"`
	Events      []Event           `json:"events"`
	Tasks       []Task            `json:"tasks"`
	Practice    Practice          `json:"practice"`
	News        []News            `json:"news"`
	Sources     map[string]string `json:"sources"` // source name -> "live" | "mock" | error text
}

type Weather struct {
	TempC    float64 `json:"tempC"`
	HiC      float64 `json:"hiC"`
	LoC      float64 `json:"loC"`
	Summary  string  `json:"summary"`
	Location string  `json:"location"`
}

type Event struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Location string    `json:"location"`
}

type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Project     string `json:"project"`
	EstimateMin int    `json:"estimateMin"`
	Done        bool   `json:"done"`
	Top         bool   `json:"top"`
	NextStep    string `json:"nextStep"`
	Sort        int    `json:"-"`
}

type Practice struct {
	Guitar  Guitar  `json:"guitar"`
	German  German  `json:"german"`
	Vocab   Vocab   `json:"vocab"`
	Reading Reading `json:"reading"`
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
	DoneToday  bool   `json:"doneToday"`
	Last7      []int  `json:"last7"`
	Level      string `json:"level"`
	DueReviews int    `json:"dueReviews"`
	Sentences  []DEEN `json:"sentences"`
	Review     []DEEN `json:"review"`
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
}

type News struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}
