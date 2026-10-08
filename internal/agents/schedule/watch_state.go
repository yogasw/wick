package schedule

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/entity"
)

// A watch that keeps running (on_match continue, or an every/cron watch
// riding out errors) must remember what it already told the session, across
// restarts, without a DB column: schedules/<id>/watch-state.json. A missing
// or unreadable file means nothing was told yet — at worst one repeat notice.

// watchErrorNotifyAfter is how many errors in a row an every/cron watch
// rides out silently before it tells the session (once per error text).
const watchErrorNotifyAfter = 5

const watchStateFile = "watch-state.json"

// watchState is the dedup memory of one watch.
type watchState struct {
	// LastHash is the hash of the last match notified (see matchHash).
	LastHash string `json:"last_hash,omitempty"`
	// Notified counts match notices sent while on_match continue.
	Notified int `json:"notified"`
	// ErrorHash is the hash of the repeated-error text last notified; cleared
	// when a run gets past the error again.
	ErrorHash string    `json:"error_hash,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// watchStatePath is the state file of m, "" for an id wick did not mint.
func watchStatePath(layout agentconfig.Layout, m entity.ScheduledMessage) string {
	if !ValidScheduleID(m.ID) || layout.BaseDir == "" {
		return ""
	}
	return filepath.Join(layout.ScheduleDir(m.ID), watchStateFile)
}

func loadWatchState(path string) watchState {
	var st watchState
	if path == "" {
		return st
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &st) != nil {
		return watchState{}
	}
	return st
}

// saveWatchState writes the file atomically (temp + rename).
func saveWatchState(path string, st watchState) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	st.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// matchHash is the dedup key of a match: the (redacted) extract of the last
// check step that has one, so fields outside it — durations, timestamps —
// never count as new; without an extract, the redacted result itself.
func matchHash(rec RunRecord, result string) string {
	if len(rec.Extract) > 0 {
		if b, err := json.Marshal(rec.Extract); err == nil { // map keys marshal sorted
			return hashText("extract:" + string(b))
		}
	}
	return hashText("result:" + strings.TrimSpace(Redact(result)))
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// WatchNotified is how many match notices a continue watch has sent (0 when
// unknown). Read from the state file of the runner in this process.
func WatchNotified(m entity.ScheduledMessage) int {
	r := running
	if r == nil {
		return 0
	}
	return loadWatchState(watchStatePath(r.layout, m)).Notified
}

func stopHint(id string) string {
	return fmt.Sprintf("Stop it with wick_schedule_message action=cancel id=%s.", id)
}

// watchContinueText is the notice of a continue watch that matched something
// new; it stays running.
func watchContinueText(m entity.ScheduledMessage, rec RunRecord, result string, n int) string {
	text := watchMatchedText(m, rec, result)
	head, rest, _ := strings.Cut(text, "\n")
	head = strings.TrimSuffix(head, "]") + fmt.Sprintf(" — notice #%d, watch still running]", n)
	return head + "\n" + rest + "\n" + stopHint(m.ID)
}

// watchContinueEndText is the one closing notice of a continue watch whose
// time limit passed.
func watchContinueEndText(m entity.ScheduledMessage, rec RunRecord, n int) string {
	return fmt.Sprintf("[watch %s timed out — outcome: timeout, run %d, %d notice(s) sent while it ran]\n%s\n%s",
		m.ID, rec.Run, n, m.Message, runsHint(m.ID))
}

// watchErrorRepeatText tells the session an every/cron watch keeps erroring;
// it keeps running.
func watchErrorRepeatText(m entity.ScheduledMessage, rec RunRecord, streak int) string {
	return fmt.Sprintf("[watch %s error repeated %d runs in a row — watch still running, run %d]\n%s\n\n%s\n%s\n%s",
		m.ID, streak, rec.Run, m.Message, fenceUntrusted("Error: "+Redact(rec.Error)), runsHint(m.ID), stopHint(m.ID))
}

// watchOnceText is the notice of a run_at watch: it ran once, whatever came
// out, and is done.
func watchOnceText(m entity.ScheduledMessage, rec RunRecord, result string) string {
	switch rec.Result {
	case entity.WatchResultMatched:
		return watchMatchedText(m, rec, result)
	case entity.WatchResultError:
		return watchFailedText(m, rec)
	}
	last := "Last run: " + rec.Result
	if rec.Reason != "" {
		last += " — " + rec.Reason
	}
	return fmt.Sprintf("[watch %s ran once at its run_at — condition not met yet, run %d]\n%s\n\n%s\n%s",
		m.ID, rec.Run, m.Message, fenceUntrusted(Redact(last)), runsHint(m.ID))
}
