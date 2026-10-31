package sources

import (
	"regexp"
	"strings"
)

// Filter decides which general news items are worth your attention.
// Exclude wins over include. Feeds with mode "all" skip the include check but still obey exclude.
type Filter struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

// DefaultFilter matches: AI and model launches, developer tooling, GitHub, climate and climate tech.
// Drops: politics, entertainment, death, war, sport.
func DefaultFilter() Filter {
	return Filter{
		Include: []string{
			"ai", "artificial intelligence", "llm", "language model", "model", "gpt", "claude", "gemini", "llama", "mistral",
			"openai", "anthropic", "deepmind", "hugging face", "agent", "copilot", "cursor",
			"github", "open source", "open-source", "developer", "api", "sdk", "cli", "framework", "library",
			"launch", "release", "announc", "unveil", "introduc", "ships", "beta", "preview", "available",
			"chip", "gpu", "nvidia", "apple", "google", "microsoft", "meta", "amazon", "linux", "kubernetes",
			"python", "typescript", "javascript", "rust", "golang", "postgres", "database", "cloud",
			"climate", "carbon", "emission", "solar", "wind power", "battery", "electric", "ev ", "renewable", "grid",
			"forest", "reforestation", "restoration", "tree", "biodiversity", "conservation", "satellite",
			"kindle", "e-ink", "robot", "startup", "security", "privacy", "browser", "chrome", "android", "ios",
		},
		Exclude: []string{
			"politic", "election", "minister", "parliament", "congress", "senate", "president", "trump", "modi", "governor",
			"bollywood", "actor", "actress", "celebrity", "film", "movie", "box office", "netflix series", "star trek",
			"dies", "dead", "death", "killed", "obituary", "funeral", "murder", "suicide",
			"war ", "warfare", "missile", "airstrike", "troops", "military", "israel", "gaza", "ukraine", "russia", "terror", "hostage",
			"cricket", "ipl", "football", "nfl", "nba", "olympic", "tennis",
		},
	}
}

var wordChars = regexp.MustCompile(`[a-z0-9]+`)

// Match reports whether text contains any of the keywords (case-insensitive substring,
// with a word boundary at the start so "ai" does not match "said" or "rain").
func matchAny(text string, keywords []string) (string, bool) {
	lower := " " + strings.ToLower(text) + " "
	for _, k := range keywords {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		idx := 0
		for {
			i := strings.Index(lower[idx:], k)
			if i < 0 {
				break
			}
			pos := idx + i
			prev := lower[pos-1]
			if prev < 'a' || prev > 'z' { // start of word
				return k, true
			}
			idx = pos + 1
		}
	}
	return "", false
}

// Decide returns keep=true, or keep=false with the reason, for a general item.
func (f Filter) Decide(title, summary string, mode string) (bool, string) {
	text := title + " " + summary
	if k, hit := matchAny(text, f.Exclude); hit {
		return false, "excluded: " + k
	}
	if mode == "all" {
		return true, ""
	}
	if _, hit := matchAny(text, f.Include); hit {
		return true, ""
	}
	return false, "no topic match"
}
