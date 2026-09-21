package event

// codex_rollout.go — recovering codex's true context LEVEL from the
// rollout file codex writes for every thread.
//
// WHY THIS FILE EXISTS
//
// `codex exec --json` reports token usage exactly once per turn, on
// turn.completed, and that number is the SUM over every model request
// the turn made — not the size of the window at the end of it. Measured
// on codex-cli 0.149.1, one turn with five requests:
//
//	turn.completed.usage.input_tokens .................. 92,842
//	rollout last_token_usage.input_tokens (real level) .. 18,874
//
// Using the first as a level overstates the meter by roughly the number
// of requests in the turn — the same trap claude's result.usage sets
// (see claude.go). Unlike claude, codex's stream carries no per-request
// usage at all: the ThreadEvent enum is thread.started / turn.started /
// turn.completed / turn.failed / item.{started,updated,completed}, and
// only turn.completed has a usage field.
//
// The number does exist, though — codex writes it to its own rollout
// journal, one `token_count` entry per request:
//
//	{"timestamp":"…","type":"event_msg","payload":{"type":"token_count",
//	 "info":{"total_token_usage":{…},"last_token_usage":{…},
//	         "model_context_window":258400}}}
//
// `last_token_usage.input_tokens` is the level (the last request's whole
// input, cache included), `total_token_usage` is the running sum the
// stream reports, and `model_context_window` is the denominator codex
// never puts on the wire. Reading the tail of that file is therefore the
// only way to give a codex session an honest meter — and it is cheap: a
// glob, a seek, and a few kilobytes.
//
// The ledger is derived data. When the file cannot be found (a relocated
// CODEX_HOME we were not told about, --ephemeral, a future format), the
// reader says so and the caller records NO level rather than falling
// back to the sum — a missing meter is recoverable, a confidently wrong
// one is not.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// codexRolloutTailBytes is how much of the rollout's end is read. One
// entry can be large (world_state carries the whole skill catalog), so
// this is sized to hold several turns' worth of entries, not one.
const codexRolloutTailBytes = 1 << 20 // 1 MiB

// codexRolloutReading is one token_count entry, reduced to the three
// numbers that matter.
type codexRolloutReading struct {
	// Level is the context level: input tokens of the LAST request,
	// cached part included (codex reports input_tokens as the total).
	Level int
	// Window is codex's own context limit for the model, 0 when absent.
	Window int
	// Sum is the running input total for the codex process, which is
	// exactly what turn.completed reports — the handle that lets the
	// caller tell "this entry is from the turn I just saw" from "this
	// entry is older".
	Sum int
}

// codexRolloutLevel returns the context level for a thread, matched
// against the sum the stream reported for the turn.
//
// The match matters because of a race: codex writes token_count a few
// milliseconds before the exec stream prints turn.completed, but nothing
// guarantees the write landed first. When the newest entry's Sum equals
// the turn's sum, the file is definitely current; when it does not, we
// wait once and look again before settling for the newest entry we have
// (one request stale is a real level, just slightly behind — still far
// closer to the truth than the turn-wide sum).
func codexRolloutLevel(home, threadID string, sum int) (codexRolloutReading, bool) {
	path := codexRolloutPath(home, threadID)
	if path == "" {
		return codexRolloutReading{}, false
	}
	best, ok := codexRolloutBest(path, sum)
	if ok && best.Sum == sum {
		return best, true
	}
	time.Sleep(codexRolloutRetryDelay)
	if retry, ok2 := codexRolloutBest(path, sum); ok2 {
		return retry, true
	}
	return best, ok
}

// codexRolloutRetryDelay is the one pause the reader takes when the
// rollout has not caught up with the stream yet. A variable so tests
// don't sleep.
var codexRolloutRetryDelay = 50 * time.Millisecond

// codexRolloutBest scans the tail and returns the entry matching sum, or
// the newest entry when none matches.
func codexRolloutBest(path string, sum int) (codexRolloutReading, bool) {
	readings := codexRolloutReadings(path)
	if len(readings) == 0 {
		return codexRolloutReading{}, false
	}
	for i := len(readings) - 1; i >= 0; i-- {
		if readings[i].Sum == sum {
			return readings[i], true
		}
	}
	return readings[len(readings)-1], true
}

// codexRolloutReadings decodes every token_count entry in the tail of
// the file, oldest first. A truncated first line (the tail rarely starts
// on a boundary) and any line that is not a token_count are skipped.
func codexRolloutReadings(path string) []codexRolloutReading {
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
	if st.Size() > codexRolloutTailBytes {
		offset = st.Size() - codexRolloutTailBytes
	}
	if _, err := f.Seek(offset, 0); err != nil {
		return nil
	}
	buf := make([]byte, st.Size()-offset)
	n, _ := f.Read(buf)
	text := string(buf[:n])
	if offset > 0 {
		// Drop the partial first line rather than failing to parse it.
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	var out []codexRolloutReading
	for _, line := range strings.Split(text, "\n") {
		// Cheap pre-filter: most lines are response items, and decoding
		// every one of them would mean parsing megabytes per turn.
		if !strings.Contains(line, `"token_count"`) {
			continue
		}
		var rec codexRolloutRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		info := rec.Payload.Info
		if rec.Payload.Type != "token_count" || info == nil || info.Last == nil {
			continue
		}
		out = append(out, codexRolloutReading{
			Level:  info.Last.InputTokens,
			Window: info.Window,
			Sum:    sumInput(info.Total),
		})
	}
	return out
}

func sumInput(u *codexUsage) int {
	if u == nil {
		return 0
	}
	return u.InputTokens
}

// codexRolloutRecord is the slice of a rollout line this file needs.
// Everything else in the journal (response items, world state, turn
// context) is deliberately not modelled — coupling to more of codex's
// format than the meter needs would break for no benefit.
type codexRolloutRecord struct {
	Payload struct {
		Type string `json:"type"`
		Info *struct {
			Total  *codexUsage `json:"total_token_usage"`
			Last   *codexUsage `json:"last_token_usage"`
			Window int         `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

// codexRolloutPath locates the rollout for a thread, newest first when
// several match (a thread id is a uuid, so that is near-impossible —
// but picking deterministically beats depending on glob order).
//
// Layout as of codex 0.149: $CODEX_HOME/sessions/YYYY/MM/DD/
// rollout-<timestamp>-<thread_id>.jsonl, appended to on every resume, so
// one thread keeps one file for its whole life. The flat fallback covers
// older layouts and anyone who set a custom sessions dir.
func codexRolloutPath(home, threadID string) string {
	if threadID == "" {
		return ""
	}
	home = codexHomeDir(home)
	if home == "" {
		return ""
	}
	sessions := filepath.Join(home, "sessions")
	patterns := []string{
		filepath.Join(sessions, "*", "*", "*", "rollout-*"+threadID+".jsonl"),
		filepath.Join(sessions, "rollout-*"+threadID+".jsonl"),
	}
	best, bestMod := "", time.Time{}
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
			if best == "" || st.ModTime().After(bestMod) {
				best, bestMod = m, st.ModTime()
			}
		}
	}
	return best
}

// codexHomeDir resolves codex's state root: the explicit value the
// spawn used, else $CODEX_HOME, else ~/.codex.
func codexHomeDir(home string) string {
	if home != "" {
		return home
	}
	if env := os.Getenv("CODEX_HOME"); env != "" {
		return env
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".codex")
}

// CodexContextLevel returns the context level codex last recorded for a
// thread, without a turn to match it against.
//
// The compaction path needs it: app-server volunteers
// thread/tokenUsage/updated on resume in codex 0.145, but not in 0.149,
// so the "before" number of a manual /compact has to come from the
// rollout instead of the wire.
//
// The newest entry is not always usable: codex writes a token_count
// after a compaction whose last_token_usage.input_tokens is 0 (nothing
// was requested, the context was rewritten), so the scan walks back to
// the newest entry that carries a real level — the turn the compaction
// is measured against. No such entry (fresh thread, relocated
// CODEX_HOME) reports false rather than 0: a missing number is
// recoverable, a wrong one is not.
func CodexContextLevel(home, threadID string) (int, bool) {
	path := codexRolloutPath(home, threadID)
	if path == "" {
		return 0, false
	}
	readings := codexRolloutReadings(path)
	for i := len(readings) - 1; i >= 0; i-- {
		if readings[i].Level > 0 {
			return readings[i].Level, true
		}
	}
	return 0, false
}
