package sources

import (
	"strings"
	"testing"
	"time"
)

const sampleICS = `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:one@test
DTSTART;TZID=Asia/Kolkata:20261102T110000
DTEND;TZID=Asia/Kolkata:20261102T113000
SUMMARY:Sync with Arnold\, ForestCloud
LOCATION:Meet
END:VEVENT
BEGIN:VEVENT
UID:allday@test
DTSTART;VALUE=DATE:20261105
DTEND;VALUE=DATE:20261106
SUMMARY:Diwali
END:VEVENT
BEGIN:VEVENT
UID:weekly@test
DTSTART:20261101T133000Z
DTEND:20261101T150000Z
RRULE:FREQ=WEEKLY;COUNT=3
SUMMARY:German class
  (online)
END:VEVENT
BEGIN:VEVENT
UID:old@test
DTSTART:20250101T100000Z
SUMMARY:Too old
END:VEVENT
END:VCALENDAR
`

func TestParseICS(t *testing.T) {
	tz, _ := time.LoadLocation("Asia/Kolkata")
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, tz)
	to := time.Date(2026, 12, 1, 0, 0, 0, 0, tz)
	events, err := ParseICS(strings.NewReader(sampleICS), tz, from, to)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]int{}
	for _, e := range events {
		byID[e.ID]++
	}
	if byID["one@test"] != 1 || byID["allday@test"] != 1 || byID["old@test"] != 0 {
		t.Fatalf("unexpected events: %+v", byID)
	}
	if n := byID["weekly@test"] + byID["weekly@test-1"] + byID["weekly@test-2"]; n != 3 {
		t.Fatalf("weekly recurrence expanded to %d, want 3", n)
	}
	for _, e := range events {
		switch e.ID {
		case "one@test":
			if e.Title != "Sync with Arnold, ForestCloud" || e.Location != "Meet" {
				t.Errorf("unescape failed: %q %q", e.Title, e.Location)
			}
			if e.Start.Hour() != 11 || e.End.Sub(e.Start) != 30*time.Minute {
				t.Errorf("bad times: %v %v", e.Start, e.End)
			}
		case "weekly@test":
			if e.Title != "German class (online)" {
				t.Errorf("folded line not joined: %q", e.Title)
			}
			if e.Start.In(tz).Hour() != 19 { // 13:30 UTC = 19:00 IST
				t.Errorf("UTC conversion wrong: %v", e.Start)
			}
		}
	}
}
