package logintty

// codex_usage.go — codex's rate-limit windows, read from the journal it
// already writes.
//
// Codex has no usage endpoint wick can call, so /usage used to answer
// "codex does not report usage limits". It does report them — just not
// over HTTP. Every turn, codex records a token_count entry into the
// rollout for that thread, and each one carries the account's current
// limits:
//
//	"rate_limits":{"primary":{"used_percent":1.0,"window_minutes":300,
//	                          "resets_at":1789793085},
//	               "secondary":{"used_percent":84.0,"window_minutes":10080,
//	                            "resets_at":1789837227}}
//
// 300 minutes is the 5-hour window and 10080 the weekly one — the same
// two claude reports, so they get the same keys and the UI labels them
// identically without knowing where they came from.
//
// The catch, and the reason ObservedAt exists: these numbers are only
// as fresh as the last codex turn on this host. Reading them costs
// nothing and can never be rate-limited, but presenting a figure from
// yesterday's run as "checked just now" would be a lie — so the reading
// carries the time codex actually observed it, and the UI shows that.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// codexRolloutScanLimit caps how many rollout files are examined, newest
// first. The limits are account-wide, so the most recent turn on the
// host answers the question; the rest are only fallbacks for when the
// newest file happens to hold no token_count yet (a session that died
// during its first request).
const codexRolloutScanLimit = 5

// codexUsageTailBytes is how much of a rollout's end is read. Entries
// can be large, so this holds several rather than one.
const codexUsageTailBytes = 512 << 10

// readCodexUsage returns codex's current rate-limit windows, or
// ErrUsageUnsupported when no rollout on this host has any.
func readCodexUsage(env []string) ([]UsageWindow, error) {
	dir := codexConfigDir(env)
	if dir == "" {
		return nil, ErrUsageUnsupported
	}
	for _, path := range codexRecentRollouts(dir, codexRolloutScanLimit) {
		if wins := codexWindowsFrom(path); len(wins) > 0 {
			return wins, nil
		}
	}
	return nil, ErrUsageUnsupported
}

// codexRecentRollouts lists rollout files newest first, at most limit of
// them.
func codexRecentRollouts(dir string, limit int) []string {
	patterns := []string{
		filepath.Join(dir, "sessions", "*", "*", "*", "rollout-*.jsonl"),
		filepath.Join(dir, "sessions", "rollout-*.jsonl"),
	}
	type entry struct {
		path string
		mod  time.Time
	}
	var found []entry
	for _, pat := range patterns {
		matches, err := filepath.Glob(pat)
		if err != nil {
			continue
		}
		for _, m := range matches {
			st, err := os.Stat(m)
			if err != nil {
				continue
			}
			found = append(found, entry{m, st.ModTime()})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod.After(found[j].mod) })
	out := make([]string, 0, limit)
	for i := 0; i < len(found) && i < limit; i++ {
		out = append(out, found[i].path)
	}
	return out
}

// codexWindowsFrom returns the windows from the LAST token_count entry
// in one rollout that carries rate limits. Nil when the file has none.
func codexWindowsFrom(path string) []UsageWindow {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	offset := int64(0)
	if st.Size() > codexUsageTailBytes {
		offset = st.Size() - codexUsageTailBytes
	}
	if _, err := f.Seek(offset, 0); err != nil {
		return nil
	}
	buf := make([]byte, st.Size()-offset)
	n, _ := f.Read(buf)
	text := string(buf[:n])
	if offset > 0 {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !strings.Contains(line, `"rate_limits"`) {
			continue
		}
		var rec codexRateLimitRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Payload.RateLimits == nil {
			continue
		}
		observed := parseCodexTime(rec.Timestamp)
		wins := codexWindows(rec.Payload.RateLimits, observed)
		if len(wins) > 0 {
			return wins
		}
	}
	return nil
}

type codexRateLimitRecord struct {
	Timestamp string `json:"timestamp"`
	Payload   struct {
		RateLimits *struct {
			Primary   *codexRateWindow `json:"primary"`
			Secondary *codexRateWindow `json:"secondary"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

type codexRateWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

func codexWindows(rl *struct {
	Primary   *codexRateWindow `json:"primary"`
	Secondary *codexRateWindow `json:"secondary"`
}, observed time.Time,
) []UsageWindow {
	var out []UsageWindow
	for _, w := range []*codexRateWindow{rl.Primary, rl.Secondary} {
		if w == nil || w.WindowMinutes <= 0 {
			continue
		}
		win := UsageWindow{
			Key:         codexWindowKey(w.WindowMinutes),
			Utilization: w.UsedPercent,
			ObservedAt:  observed,
		}
		if w.ResetsAt > 0 {
			win.ResetsAt = time.Unix(w.ResetsAt, 0).UTC()
		}
		out = append(out, win)
	}
	return out
}

// codexWindowKey maps a window length to the key the UI already knows.
// An unfamiliar length keeps its own name rather than being forced into
// one of the two — the label falls back to printing the key, which is
// better than calling a 24-hour window "Weekly".
func codexWindowKey(minutes int) string {
	switch minutes {
	case 300:
		return "five_hour"
	case 10080:
		return "seven_day"
	default:
		return strconv.Itoa(minutes) + "m"
	}
}

func parseCodexTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
