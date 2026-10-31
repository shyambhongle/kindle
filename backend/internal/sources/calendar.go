package sources

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shyam/dashboard/internal/model"
)

// Calendar reads a Google Calendar "secret address in iCal format" (or any ICS URL).
// It handles single events and all-day events. Recurring events (RRULE) are expanded
// for simple DAILY and WEEKLY rules within the window; other rules show their first occurrence only.
type Calendar struct {
	URL string
	TZ  *time.Location
}

func (c *Calendar) Configured() bool { return c.URL != "" }

// Events returns events between from and to.
func (c *Calendar) Events(ctx context.Context, from, to time.Time) ([]model.Event, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.URL, nil)
	resp, err := Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("calendar HTTP %d", resp.StatusCode)
	}
	return ParseICS(io.LimitReader(resp.Body, maxBody), c.TZ, from, to)
}

type rawEvent struct {
	uid, summary, location, dtstart, dtend, rrule string
	tzStart, tzEnd                                string
}

// ParseICS is a small, dependency-free VEVENT parser (unfolds lines, reads the fields we show).
func ParseICS(r io.Reader, tz *time.Location, from, to time.Time) ([]model.Event, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var lines []string
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += l[1:] // folded line
			continue
		}
		lines = append(lines, l)
	}
	var out []model.Event
	var cur *rawEvent
	for _, l := range lines {
		switch {
		case l == "BEGIN:VEVENT":
			cur = &rawEvent{}
		case l == "END:VEVENT":
			if cur != nil {
				out = append(out, expand(cur, tz, from, to)...)
			}
			cur = nil
		case cur != nil:
			name, val, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			key, params, _ := strings.Cut(name, ";")
			tzid := ""
			for _, p := range strings.Split(params, ";") {
				if strings.HasPrefix(p, "TZID=") {
					tzid = strings.TrimPrefix(p, "TZID=")
				}
			}
			switch key {
			case "UID":
				cur.uid = val
			case "SUMMARY":
				cur.summary = unescape(val)
			case "LOCATION":
				cur.location = unescape(val)
			case "DTSTART":
				cur.dtstart, cur.tzStart = val, tzid
			case "DTEND":
				cur.dtend, cur.tzEnd = val, tzid
			case "RRULE":
				cur.rrule = val
			}
		}
	}
	return out, nil
}

func unescape(s string) string {
	s = strings.ReplaceAll(s, `\,`, ",")
	s = strings.ReplaceAll(s, `\;`, ";")
	s = strings.ReplaceAll(s, `\n`, " ")
	return strings.ReplaceAll(s, `\\`, `\`)
}

func parseICSTime(v, tzid string, def *time.Location) (time.Time, bool, error) {
	loc := def
	if tzid != "" {
		if l, err := time.LoadLocation(tzid); err == nil {
			loc = l
		}
	}
	switch {
	case len(v) == 8: // all day: 20261031
		t, err := time.ParseInLocation("20060102", v, def)
		return t, true, err
	case strings.HasSuffix(v, "Z"):
		t, err := time.Parse("20060102T150405Z", v)
		return t.In(def), false, err
	default:
		t, err := time.ParseInLocation("20060102T150405", v, loc)
		return t.In(def), false, err
	}
}

func expand(e *rawEvent, tz *time.Location, from, to time.Time) []model.Event {
	start, allDay, err := parseICSTime(e.dtstart, e.tzStart, tz)
	if err != nil {
		return nil
	}
	var end time.Time
	if e.dtend != "" {
		end, _, _ = parseICSTime(e.dtend, e.tzEnd, tz)
	}
	if end.IsZero() {
		if allDay {
			end = start.Add(24 * time.Hour)
		} else {
			end = start.Add(time.Hour)
		}
	}
	dur := end.Sub(start)
	id := e.uid
	if id == "" {
		h := sha1.Sum([]byte(e.summary + e.dtstart))
		id = hex.EncodeToString(h[:8])
	}
	mk := func(s time.Time, n int) model.Event {
		eid := id
		if n > 0 {
			eid = fmt.Sprintf("%s-%d", id, n)
		}
		return model.Event{ID: eid, Title: e.summary, Start: s, End: s.Add(dur), Location: e.location}
	}
	// recurrence: support FREQ=DAILY / WEEKLY with optional INTERVAL, COUNT, UNTIL
	if e.rrule == "" {
		if !start.After(to) && !end.Before(from) {
			return []model.Event{mk(start, 0)}
		}
		return nil
	}
	rule := map[string]string{}
	for _, p := range strings.Split(e.rrule, ";") {
		k, v, _ := strings.Cut(p, "=")
		rule[k] = v
	}
	step := 24 * time.Hour
	switch rule["FREQ"] {
	case "DAILY":
	case "WEEKLY":
		step = 7 * 24 * time.Hour
	default:
		if !start.After(to) && !end.Before(from) {
			return []model.Event{mk(start, 0)}
		}
		return nil
	}
	interval := 1
	fmt.Sscanf(rule["INTERVAL"], "%d", &interval)
	if interval < 1 {
		interval = 1
	}
	count := 1000
	fmt.Sscanf(rule["COUNT"], "%d", &count)
	until := to
	if u := rule["UNTIL"]; u != "" {
		if t, _, err := parseICSTime(u, "", tz); err == nil && t.Before(until) {
			until = t
		}
	}
	var out []model.Event
	for n, s := 0, start; n < count && !s.After(until); n, s = n+1, s.Add(time.Duration(interval)*step) {
		if !s.Add(dur).Before(from) {
			out = append(out, mk(s, n))
		}
		if len(out) > 60 {
			break
		}
	}
	return out
}

// DummyEvents gives a realistic week when no calendar is configured.
func DummyEvents(tz *time.Location) []model.Event {
	now := time.Now().In(tz)
	at := func(dayOff, h, m int) time.Time {
		return time.Date(now.Year(), now.Month(), now.Day()+dayOff, h, m, 0, 0, tz)
	}
	ev := func(id, title string, dayOff, h, m, durMin int, loc string) model.Event {
		s := at(dayOff, h, m)
		return model.Event{ID: id, Title: title, Start: s, End: s.Add(time.Duration(durMin) * time.Minute), Location: loc}
	}
	return []model.Event{
		ev("e1", "ForestCloud sync with Arnold", 0, 11, 0, 30, "Meet"),
		ev("e2", "Guitar lesson", 0, 18, 0, 60, "Home"),
		ev("e3", "Dentist", 2, 9, 30, 45, "Clinic"),
		ev("e4", "Academy planning call", 4, 14, 0, 60, "Zoom"),
		ev("e5", "German class", 7, 19, 0, 90, "Online"),
		ev("e6", "Flight to Mumbai", 12, 7, 10, 80, "PNQ"),
		ev("e7", "Team retro", -3, 16, 0, 60, "Meet"),
	}
}
