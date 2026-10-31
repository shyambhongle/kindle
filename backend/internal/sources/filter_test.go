package sources

import "testing"

func TestFilterDecide(t *testing.T) {
	f := DefaultFilter()
	keep := []string{
		"Gemini 4 Carbon: Our next frontier model",
		"Webhook IP ranges added to the Meta API",
		"Meta strikes deal to help bring a low-carbon cement plant to Missouri",
		"Show HN: a Rust library for parsing ICS files",
	}
	drop := []string{
		"Through Captain Jean-Luc Picard, Patrick Stewart taught us the measure of a man", // star trek
		"Minister announces new AI policy for elections",                                  // politics wins over AI
		"Bollywood actor dies at 78",
		"Given a chance, women-led microenterprises uplift Nepali communities",            // no topic match
	}
	for _, s := range keep {
		if ok, why := f.Decide(s, "", "keywords"); !ok {
			t.Errorf("should keep %q: %s", s, why)
		}
	}
	for _, s := range drop {
		if ok, _ := f.Decide(s, "", "keywords"); ok {
			t.Errorf("should drop %q", s)
		}
	}
	// "all" mode still obeys exclude, skips include
	if ok, _ := f.Decide("Quarterly notes from the team", "", "all"); !ok {
		t.Error("all mode should keep unmatched topics")
	}
	if ok, _ := f.Decide("Our CEO on the war in Ukraine", "", "all"); ok {
		t.Error("all mode should still exclude")
	}
	// word boundary: "ai" must not match "said"/"rain"
	if ok, _ := f.Decide("He said it would rain tomorrow", "", "keywords"); ok {
		t.Error("ai should not match inside said/rain")
	}
}
