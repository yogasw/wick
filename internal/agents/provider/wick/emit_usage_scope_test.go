package wick

import "testing"

// TestDoneLineUsageIsTurnScoped: the engine's numbers are already per
// turn, and one ClaudeParser reads every turn of the process. Two equal
// turns must both be charged in full, not differenced to zero.
func TestDoneLineUsageIsTurnScoped(t *testing.T) {
	tt := turnTokens{freshIn: 100, cacheRead: 50, output: 7, lastIn: 100, lastCache: 50}
	line := string(doneLineUsage("ok", "gemini-2.5", tt))
	evs := collectParsed(t, []string{line, line})
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
	for i, ev := range evs {
		u := ev.Usage
		if u == nil || u.Input != 100 || u.CacheRead != 50 || u.Output != 7 {
			t.Fatalf("turn %d: want the turn's own numbers, got %+v", i, u)
		}
	}
}
