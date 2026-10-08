package schedule

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// Run history is kept as files, not rows — for watches AND message
// schedules — one JSON per run at
//
//	<agents base>/schedules/<schedule_id>/runs/<run_id>.json
//
// beside workflows/ (agentconfig.Layout.ScheduleRunsDir), outside every
// project's working folder, so writing it never touches a repo the Source
// panel watches. A watch can tick every 10 seconds, and a table taking 8,640
// rows a day per watch for a debugging aid is the wrong trade.
//
// Pruning is hysteretic: a folder grows to runsPruneHigh files, then the
// oldest are dropped down to runsPruneKeep. The count lives in memory (one
// ReadDir the first time a folder is used and after each prune), so a run
// never lists its folder.

const (
	runsPruneHigh = 100
	runsPruneKeep = 50
	// runsListDefault is how many runs a listing returns by default.
	runsListDefault = 50
)

// ErrRunNotFound is returned for a run id with no file.
var ErrRunNotFound = errors.New("schedule run not found")

// WatchCwd is the directory a watch's bash steps run in: the target project's
// working folder, else the target session's own cwd. "" when none resolves.
func WatchCwd(layout agentconfig.Layout, m entity.ScheduledMessage) string {
	if m.ProjectID != "" {
		if dir, err := project.ResolvePath(layout, m.ProjectID); err == nil {
			return dir
		}
	}
	if m.SessionID != "" {
		if sess, err := session.Load(layout, m.SessionID); err == nil {
			if dir, err := session.Cwd(layout, sess); err == nil {
				return dir
			}
		}
	}
	return ""
}

// WatchRunsDir is where a schedule's run files go. "" when the id is not one
// wick minted: an id becomes part of the path, so anything else is refused
// rather than cleaned.
func WatchRunsDir(layout agentconfig.Layout, m entity.ScheduledMessage) string {
	if !ValidScheduleID(m.ID) || layout.BaseDir == "" {
		return ""
	}
	return layout.ScheduleRunsDir(m.ID)
}

// runID names a run file (id + ".json"): an RFC3339-nano UTC timestamp with
// the colons swapped for dashes (a colon is not a legal file name everywhere).
// Fixed-width, so it sorts lexically in time order — what prune and list
// rely on.
func runID(t time.Time) string { return t.UTC().Format("2006-01-02T15-04-05.000000000Z") }

// runCounts is the in-memory file count per runs folder (see the package
// note on pruning).
var runCounts = struct {
	mu sync.Mutex
	n  map[string]int
}{n: map[string]int{}}

// writeRun stores rec and prunes the folder once it reaches runsPruneHigh.
func writeRun(dir string, rec *RunRecord) error {
	if dir == "" {
		return errors.New("no run history dir")
	}
	// Owner-only: run output can carry whatever a script or API returned.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	rec.ID = runID(rec.StartedAt)
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+rec.ID+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, rec.ID+".json")); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	runCounts.mu.Lock()
	defer runCounts.mu.Unlock()
	n, known := runCounts.n[dir]
	if !known {
		files, err := runFiles(dir)
		if err != nil {
			return err
		}
		n = len(files) // includes the file just written
	} else {
		n++
	}
	if n >= runsPruneHigh {
		left, err := pruneRuns(dir, runsPruneKeep)
		if err != nil {
			delete(runCounts.n, dir)
			return err
		}
		n = left
	}
	runCounts.n[dir] = n
	return nil
}

// forgetRunCount drops the cached counts under dir (it was removed).
func forgetRunCount(dir string) {
	runCounts.mu.Lock()
	defer runCounts.mu.Unlock()
	for d := range runCounts.n {
		if d == dir || strings.HasPrefix(d, dir+string(os.PathSeparator)) {
			delete(runCounts.n, d)
		}
	}
}

// runFiles lists the run files in dir, oldest first.
func runFiles(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".json") || strings.HasPrefix(n, ".") {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// pruneRuns deletes all but the newest keep run files and reports how many
// remain, from a fresh listing.
func pruneRuns(dir string, keep int) (int, error) {
	files, err := runFiles(dir)
	if err != nil {
		return 0, err
	}
	for len(files) > keep {
		_ = os.Remove(filepath.Join(dir, files[0]))
		files = files[1:]
	}
	after, err := runFiles(dir)
	return len(after), err
}

// RunSummary is one row of the run list: everything but step output.
type RunSummary struct {
	ID         string     `json:"id"`
	Type       string     `json:"type,omitempty"`
	Run        int        `json:"run"`
	StartedAt  time.Time  `json:"started_at"`
	DurationMs int64      `json:"duration_ms"`
	Result     string     `json:"result"`
	Manual     bool       `json:"manual"`
	Outcome    string     `json:"outcome,omitempty"`
	SessionID  string     `json:"session_id,omitempty"`
	Error      string     `json:"error,omitempty"`
	Steps      int        `json:"steps"`
	StoppedAt  *StopPoint `json:"stopped_at,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	StepsRev   int        `json:"steps_rev,omitempty"`
	DryRun     bool       `json:"dry_run,omitempty"`
}

// ListWatchRuns returns a schedule's runs, newest first, at most limit
// (≤ 0 means runsListDefault; capped at runsPruneHigh). Empty for a schedule
// with no file history yet — callers that can reconstruct older history
// (message schedules, from the conversation) fall back to that.
func ListWatchRuns(layout agentconfig.Layout, m entity.ScheduledMessage, limit int) ([]RunSummary, error) {
	return listRunsFiltered(layout, m, limit, "")
}

// listRunsFiltered is ListWatchRuns keeping only runs whose result is
// result ("" keeps all).
func listRunsFiltered(layout agentconfig.Layout, m entity.ScheduledMessage, limit int, result string) ([]RunSummary, error) {
	if limit <= 0 {
		limit = runsListDefault
	}
	if limit > runsPruneHigh {
		limit = runsPruneHigh
	}
	dir := WatchRunsDir(layout, m)
	if dir == "" {
		return nil, nil
	}
	files, err := runFiles(dir)
	if err != nil {
		return nil, err
	}
	out := make([]RunSummary, 0, min(len(files), limit))
	for i := len(files) - 1; i >= 0 && len(out) < limit; i-- {
		rec, err := readRun(filepath.Join(dir, files[i]))
		if err != nil || (result != "" && rec.Result != result) {
			continue
		}
		out = append(out, RunSummary{ID: rec.ID, Type: rec.Type, Run: rec.Run, StartedAt: rec.StartedAt, DurationMs: rec.DurationMs,
			Result: rec.Result, Manual: rec.Manual, Outcome: rec.Outcome, SessionID: rec.SessionID, Error: firstLineOf(rec.Error), Steps: len(rec.Steps),
			StoppedAt: rec.StoppedAt, Reason: rec.Reason, StepsRev: rec.StepsRev, DryRun: rec.DryRun})
	}
	return out, nil
}

// GetWatchRun loads one run by id.
func GetWatchRun(layout agentconfig.Layout, m entity.ScheduledMessage, id string) (*RunRecord, error) {
	dir := WatchRunsDir(layout, m)
	if dir == "" || !ValidRunID(id) {
		return nil, ErrRunNotFound
	}
	rec, err := readRun(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil, ErrRunNotFound
	}
	return rec, nil
}

func readRun(path string) (*RunRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec RunRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// RemoveWatchRuns deletes a schedule's folder (schedules/<id>/), for a
// schedule being deleted. Any type: message schedules keep history too.
func RemoveWatchRuns(layout agentconfig.Layout, m entity.ScheduledMessage) {
	if !ValidScheduleID(m.ID) || layout.BaseDir == "" {
		return
	}
	dir := layout.ScheduleDir(m.ID)
	_ = os.RemoveAll(dir)
	forgetRunCount(dir)
}

// Message-schedule run results.
const (
	RunResultDelivered = "delivered"
	RunResultFailed    = "failed"
)

// messageRunSnippet caps the preview of a message schedule's text kept in
// its run record — enough to recognise the run, never the whole prompt.
const messageRunSnippet = 200

// messageRunRecord is the history record of one message-schedule fire.
func messageRunRecord(m entity.ScheduledMessage, started time.Time, target string, sendErr error) RunRecord {
	rec := RunRecord{
		Type: entity.ScheduledTypeMessage, ScheduleID: m.ID, StartedAt: started.UTC(), FinishedAt: time.Now().UTC(),
		Manual: m.ManualFire, SessionID: target, Result: RunResultDelivered,
	}
	rec.DurationMs = rec.FinishedAt.Sub(rec.StartedAt).Milliseconds()
	if m.ManualFire {
		rec.Run = m.ManualRuns
	} else {
		rec.Run = m.RunCount
	}
	if r := []rune(m.Message); len(r) > messageRunSnippet {
		rec.Message = Redact(string(r[:messageRunSnippet]) + "…")
	} else {
		rec.Message = Redact(m.Message)
	}
	if sendErr != nil {
		rec.Result = RunResultFailed
		rec.Error = Redact(sendErr.Error())
	}
	return rec
}

// sweepTerminalAge is how long a finished schedule's history is kept.
const sweepTerminalAge = 30 * 24 * time.Hour

// sweepScheduleDirs removes schedules/<id>/ folders whose id is not in keep
// (orphans, and schedules finished longer than sweepTerminalAge ago). Only
// names shaped like a schedule id are touched.
func sweepScheduleDirs(layout agentconfig.Layout, keep map[string]bool) int {
	if layout.BaseDir == "" {
		return 0
	}
	ents, err := os.ReadDir(layout.SchedulesDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		id := e.Name()
		if !e.IsDir() || !ValidScheduleID(id) || keep[id] {
			continue
		}
		dir := layout.ScheduleDir(id)
		if os.RemoveAll(dir) == nil {
			forgetRunCount(dir)
			n++
		}
	}
	return n
}

// LegacyRunReader reconstructs a schedule's runs from somewhere other than
// the history files — the conversation of the session it fired into, for
// message schedules that ran before file history existed. Newest first.
type LegacyRunReader func(m entity.ScheduledMessage) []RunSummary

var legacyRuns atomic.Pointer[LegacyRunReader]

// SetLegacyRunReader installs the fallback ListRuns uses when a schedule has
// no history files yet. The agents tool registers it; nil removes it.
func SetLegacyRunReader(fn LegacyRunReader) {
	if fn == nil {
		legacyRuns.Store(nil)
		return
	}
	legacyRuns.Store(&fn)
}

// ListRuns is every history reader's entry point: the history files first,
// and only when there are none, the legacy reconstruction.
//
// result, when set, keeps only runs with that result (e.g. "error").
func ListRuns(layout agentconfig.Layout, m entity.ScheduledMessage, limit int, result string) ([]RunSummary, error) {
	runs, err := listRunsFiltered(layout, m, limit, result)
	if err != nil || len(runs) > 0 {
		return runs, err
	}
	if files, _ := runFiles(WatchRunsDir(layout, m)); len(files) > 0 {
		return runs, nil // history exists, the filter just matched nothing
	}
	if fn := legacyRuns.Load(); fn != nil {
		runs = (*fn)(m)
		if result != "" {
			kept := runs[:0]
			for _, r := range runs {
				if r.Result == result {
					kept = append(kept, r)
				}
			}
			runs = kept
		}
		if limit <= 0 {
			limit = runsListDefault
		}
		if len(runs) > limit {
			runs = runs[:limit]
		}
	}
	return runs, nil
}
