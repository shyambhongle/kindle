package sources

import "testing"

func TestIsAlert(t *testing.T) {
	yes := []string{
		"Cursor Removes Malicious Claude Code 2.1.313 Extension From Its Marketplace",
		"npm package compromised to steal credentials",
		"CVE-2026-1234 in a popular build tool allows remote code execution",
		"Typosquatted PyPI package drops an infostealer",
	}
	no := []string{
		"How We Review Dependency Updates at Socket",
		"OpenCode User Hit with Hostile Backlash After Reporting Free AI Loophole",
		"Introducing our new pricing page",
	}
	for _, s := range yes {
		if !IsAlert(s, "") {
			t.Errorf("should be an alert: %q", s)
		}
	}
	for _, s := range no {
		if IsAlert(s, "") {
			t.Errorf("should not be an alert: %q", s)
		}
	}
}
