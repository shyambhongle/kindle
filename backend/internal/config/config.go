// Package config loads settings from environment variables (and an optional .env file).
package config

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr      string        // e.g. "0.0.0.0:8080" or "192.168.1.20:8080"
	DatabaseURL     string        // postgres://user:pass@localhost:5432/dashboard
	AccessToken     string        // long random string, part of every URL
	DataDir         string        // folder with the seed datasets
	Timezone        *time.Location
	CollectEvery    time.Duration
	Latitude        float64
	Longitude       float64
	WeatherLocation string
	NotionToken     string
	NotionTasksDB   string
	NotionStatus    map[string]string // property, top, next, backlog, hidden, done
	NotionBaseURL   string            // override for tests
	WeatherBaseURL  string            // override for tests (forecast and geocoding)
	CalendarICSURL  string
	GuitarStartDate string
	AnthropicKey    string
	AnthropicModel  string
	AnthropicURL    string // override for tests
	GermanStartDate string // overrides the course file's start_date when set
	BackupDir       string // where pg_dump backups go
	DailyBackup     bool
	Mock            bool // force dummy data for every external source
}

// Load reads .env (if present) then the environment. Environment wins.
func Load() (*Config, error) {
	loadDotEnv(".env")
	loadDotEnv("secrets/.env")

	c := &Config{
		ListenAddr:      env("LISTEN_ADDR", "0.0.0.0:8080"),
		DatabaseURL:     env("DATABASE_URL", "postgres://dashboard:dashboard@localhost:5432/dashboard?sslmode=disable"),
		AccessToken:     env("ACCESS_TOKEN", ""),
		DataDir:         env("DATA_DIR", "data"),
		WeatherLocation: env("WEATHER_LOCATION", "Pune"),
		NotionToken:     env("NOTION_TOKEN", ""),
		NotionTasksDB:   strings.ReplaceAll(env("NOTION_TASKS_DB", ""), "-", ""),
		NotionBaseURL:   env("NOTION_BASE_URL", "https://api.notion.com"),
		WeatherBaseURL:  env("WEATHER_BASE_URL", ""),
		NotionStatus: map[string]string{
			"property": env("NOTION_STATUS_PROP", "Status"),
			"top":      env("NOTION_STATUS_TOP", "Priority"),
			"next":     env("NOTION_STATUS_NEXT", "In Progress"),
			"backlog":  env("NOTION_STATUS_BACKLOG", "All Task"),
			"hidden":   env("NOTION_STATUS_HIDDEN", "Parked"),
			"done":     env("NOTION_STATUS_DONE", "Done"),
		},
		CalendarICSURL:  env("CALENDAR_ICS_URL", ""),
		AnthropicKey:    env("ANTHROPIC_API_KEY", ""),
		AnthropicModel:  env("ANTHROPIC_MODEL", "claude-sonnet-4-5"),
		AnthropicURL:    env("ANTHROPIC_BASE_URL", ""),
		GermanStartDate: env("GERMAN_START_DATE", ""),
		GuitarStartDate: env("GUITAR_START_DATE", ""),
		BackupDir:       env("BACKUP_DIR", "backups"),
		DailyBackup:     env("DAILY_BACKUP", "true") != "false",
		Mock:            env("MOCK", "false") == "true",
	}
	var err error
	if c.Timezone, err = time.LoadLocation(env("TZ", "Asia/Kolkata")); err != nil {
		return nil, fmt.Errorf("TZ: %w", err)
	}
	if c.CollectEvery, err = time.ParseDuration(env("COLLECT_EVERY", "15m")); err != nil {
		return nil, fmt.Errorf("COLLECT_EVERY: %w", err)
	}
	if c.Latitude, err = strconv.ParseFloat(env("WEATHER_LAT", "18.5204"), 64); err != nil {
		return nil, fmt.Errorf("WEATHER_LAT: %w", err)
	}
	if c.Longitude, err = strconv.ParseFloat(env("WEATHER_LON", "73.8567"), 64); err != nil {
		return nil, fmt.Errorf("WEATHER_LON: %w", err)
	}
	if len(c.AccessToken) < 24 {
		return nil, errors.New("ACCESS_TOKEN must be set and at least 24 characters (run: dashboard token)")
	}
	return c, nil
}

// NewToken returns a 32 byte random hex token for ACCESS_TOKEN.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}
