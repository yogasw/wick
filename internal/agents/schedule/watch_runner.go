package schedule

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/upgrade"
)

// Watches are scheduled IN MEMORY. The database behind wick is remote
// (tens of ms per round trip), and a watch can tick every 10 seconds, so a
// tick that only learns "still pending" must not touch it:
//
//   - the live watch set is loaded with ONE query per refresh (every 30s, and
//     right away when a schedule is edited in-process — see Store.changed)
//   - due-ness, run_count, last_run_at, last_result and the error streak live
//     in memory between writes; the run history is files
//   - the row is written only when the result CHANGES, on a terminal state
//     (matched → done, failed, exhausted), after a manual run, and as a
//     checkpoint at most once a minute per watch while it keeps pending
//   - a DB lease, renewed on refresh (not per tick), makes sure only one wick
//     process schedules watches; a restart resumes from the last checkpoint
//     (run_count may lag by up to a minute, which is harmless)

const (
	watchPollInterval    = 5 * time.Second
	watchRefreshInterval = 30 * time.Second
	watchFlushInterval   = 60 * time.Second
	// watchLeaseTTL outlives two missed refreshes before another process may
	// take over; a clean shutdown releases it immediately.
	watchLeaseTTL  = 90 * time.Second
	watchLeaseName = "watch-runner"
	// watchConcurrency caps watch runs in flight host-wide (2 vCPU hosts).
	watchConcurrency = 2
	// watchAccessTTL is how long a passed identity / Bash check is trusted
	// before the next run re-checks it against the database.
	watchAccessTTL = 30 * time.Second
)

// watchEntry is one live watch as the runner holds it.
type watchEntry struct {
	m entity.ScheduledMessage
	// dbUpdatedAt is the updated_at of the row as last loaded or written by
	// this runner; a refresh that sees a different value knows somebody else
	// edited the row.
	dbUpdatedAt time.Time
	next        time.Time
	// pendingNext is the real next fire while a manual run borrows next.
	pendingNext time.Time
	running     bool
	gone        bool
	dirty       bool
	lastFlush   time.Time
	accessUntil time.Time
}

type watchSet struct {
	mu      sync.Mutex
	entries map[string]*watchEntry
	holder  string
	leased  bool
	// lastSweep is when history was last swept (maybeSweep).
	lastSweep time.Time
}

func newWatchSet() *watchSet {
	return &watchSet{entries: map[string]*watchEntry{}, holder: "wr_" + uuid.NewString()[:13]}
}

// WithConnectorExecutor installs the executor watch connector steps run
// through. The server passes one built on the wick_execute path (visibility,
// agent scope, gate, audit); without it connector steps fail.
func (r *Runner) WithConnectorExecutor(exec ConnectorExecutor) *Runner {
	r.connExec = exec
	return r
}

// sameStamp compares updated_at values across a DB round trip, which may
// drop sub-microsecond precision.
func sameStamp(a, b time.Time) bool {
	d := a.Sub(b)
	return d < time.Millisecond && d > -time.Millisecond
}

// stamp is an updated_at value that survives the round trip unchanged.
func stamp() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// refreshWatches renews the lease and reloads the live watch set.
func (r *Runner) refreshWatches(ctx context.Context, l zerologLogger) {
	ws := r.watches
	ok, err := r.store.AcquireLease(ctx, watchLeaseName, ws.holder, watchLeaseTTL)
	if err != nil {
		// Unknown is treated as lost: another process may have taken the
		// lease meanwhile, and running on would schedule watches twice. The
		// next refresh retries.
		l.Warn().Err(err).Msg("watch lease failed — scheduling stopped until it is renewed")
		ok = false
	}
	ws.mu.Lock()
	ws.leased = ok
	if !ok {
		// Another process runs the watches. Drop ours (runs in flight finish
		// and see gone) so nothing is scheduled twice.
		for id, e := range ws.entries {
			e.gone = true
			delete(ws.entries, id)
		}
		ws.mu.Unlock()
		return
	}
	ws.mu.Unlock()
	r.maybeSweep(ctx, l)

	rows, err := r.store.LoadLiveWatches(ctx)
	if err != nil {
		l.Warn().Err(err).Msg("load watches failed")
		return
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row.ID] = true
		e, ok := ws.entries[row.ID]
		if !ok {
			e = &watchEntry{m: row, dbUpdatedAt: row.UpdatedAt, next: row.RunAt, lastFlush: time.Now()}
			if row.ManualFire && row.PendingRunAt != nil {
				e.pendingNext = *row.PendingRunAt
			}
			ws.entries[row.ID] = e
			continue
		}
		if sameStamp(row.UpdatedAt, e.dbUpdatedAt) {
			continue // our own last write — memory is ahead of the row
		}
		// Edited elsewhere (reschedule, pause/resume, steps, run now): adopt
		// the definition and timing, keep the counters memory is ahead on.
		mem := e.m
		e.m = row
		if mem.RunCount > row.RunCount {
			e.m.RunCount = mem.RunCount
		}
		if mem.ManualRuns > row.ManualRuns {
			e.m.ManualRuns = mem.ManualRuns
		}
		if mem.LastRunAt != nil && (row.LastRunAt == nil || mem.LastRunAt.After(*row.LastRunAt)) {
			e.m.LastRunAt = mem.LastRunAt
		}
		e.m.LastResult, e.m.ConsecutiveErrors, e.m.LastError = mem.LastResult, mem.ConsecutiveErrors, mem.LastError
		if row.ManualFire && !mem.ManualFire {
			// Run now: fire immediately, then return to the slot memory had.
			e.pendingNext = e.next
		}
		e.next = row.RunAt
		e.dbUpdatedAt = row.UpdatedAt
		e.accessUntil = time.Time{} // re-check access after any edit
	}
	for id, e := range ws.entries {
		if !seen[id] {
			e.gone = true // cancelled / deleted / finished elsewhere
			delete(ws.entries, id)
		}
	}
}

// dueWatches marks and returns the watches due at now.
func (r *Runner) dueWatches(now time.Time) []*watchEntry {
	ws := r.watches
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if !ws.leased {
		return nil
	}
	var due []*watchEntry
	for _, e := range ws.entries {
		if e.running || e.gone || e.m.Paused || e.next.IsZero() || now.Before(e.next) {
			continue
		}
		e.running = true
		due = append(due, e)
	}
	return due
}

// tickWatch runs every due watch off the loop (bounded by watchSem) and
// writes the once-a-minute checkpoints for watches that kept pending.
func (r *Runner) tickWatch(ctx context.Context, l zerologLogger) {
	// Same rule as tick: no new fires while this process is draining.
	if upgrade.Draining() {
		return
	}
	for _, e := range r.dueWatches(time.Now()) {
		e := e
		r.active.Add(1)
		go func() {
			defer r.active.Add(-1)
			select {
			case r.watchSem <- struct{}{}:
			case <-ctx.Done():
				r.watches.mu.Lock()
				e.running = false
				r.watches.mu.Unlock()
				return
			}
			defer func() { <-r.watchSem }()
			r.runWatch(ctx, l, e)
		}()
	}
	r.flushWatches(ctx, l, false)
}

// flushWatches checkpoints dirty, idle entries — at most once a minute each,
// or all of them when force (shutdown).
func (r *Runner) flushWatches(ctx context.Context, l zerologLogger, force bool) {
	type cp struct {
		id       string
		runs     int
		last     *time.Time
		next, at time.Time
		e        *watchEntry
	}
	var todo []cp
	now := time.Now()
	r.watches.mu.Lock()
	for _, e := range r.watches.entries {
		if !e.dirty || e.running || e.gone || (!force && now.Sub(e.lastFlush) < watchFlushInterval) {
			continue
		}
		at := stamp()
		todo = append(todo, cp{e.m.ID, e.m.RunCount, e.m.LastRunAt, e.next, at, e})
		e.dirty, e.lastFlush, e.dbUpdatedAt = false, now, at
	}
	r.watches.mu.Unlock()
	for _, c := range todo {
		if err := r.store.FlushWatchProgress(ctx, c.id, c.runs, c.last, c.next, c.at); err != nil {
			l.Warn().Str("id", c.id).Err(err).Msg("watch checkpoint failed")
		}
	}
}

// watchAccessOK re-checks, at most every watchAccessTTL, that the watch
// still has an identity to run as and — for bash steps — that its agent may
// still run Bash. A revoked user or a Bash switch turned off makes the run an
// error; nothing ever falls back to a system identity.
func (r *Runner) watchAccessOK(ctx context.Context, e *watchEntry, steps []Step) string {
	if time.Now().Before(e.accessUntil) {
		return ""
	}
	if denied := r.checkAccess(ctx, e.m, steps); denied != "" {
		return denied
	}
	e.accessUntil = time.Now().Add(watchAccessTTL)
	return ""
}

// checkAccess is the uncached identity + Bash check (watchAccessOK, test runs).
func (r *Runner) checkAccess(ctx context.Context, m entity.ScheduledMessage, steps []Step) string {
	runAs := m.EffectiveRunAsUser()
	if runAs == "" {
		return "watch has no owner or run-as user"
	}
	if r.runAsUsable != nil && !r.runAsUsable(ctx, runAs) {
		return "run-as user " + runAs + " is missing or not approved"
	}
	// The same Bash rule as when the steps were stored, against the session
	// that asked for the watch as well as its target, and the creator.
	if err := CheckBashAllowed(ctx, steps, []string{m.SessionID, m.SourceSessionID}, m.ProjectID, m.OwnerUserID); err != nil {
		return err.Error()
	}
	return ""
}

// ErrNoRunner is returned by TestWatch when this process runs no scheduler.
var ErrNoRunner = errors.New("watch tests need the wick server (no schedule runner in this process)")

// ErrNotWatch is returned by TestWatch for a schedule that is not a watch.
var ErrNotWatch = errors.New("only a type=watch schedule can be tested")

// ErrTestBusy is returned by TestWatch while another test runs.
var ErrTestBusy = errors.New("another watch test is running — try again in a moment")

// TestWatch runs m's steps — or override, when given — ONCE, now, with the
// same identity, Bash and cwd rules as a real tick, and returns the full
// record. It is a dry run: nothing is delivered, the row is not touched (no
// status, counters, result or error streak), and the record goes to the
// history marked manual + dry_run. Override steps are never stored.
func TestWatch(ctx context.Context, m entity.ScheduledMessage, override []Step) (*RunRecord, error) {
	r := running
	if r == nil {
		return nil, ErrNoRunner
	}
	return r.testWatch(ctx, m, override)
}

func (r *Runner) testWatch(ctx context.Context, m entity.ScheduledMessage, override []Step) (*RunRecord, error) {
	if !m.IsWatch() {
		return nil, ErrNotWatch
	}
	steps := override
	if steps == nil {
		var err error
		if steps, err = ParseSteps(m.Steps); err != nil {
			return nil, err
		}
	}
	if err := ValidateSteps(steps); err != nil {
		return nil, err
	}
	select {
	case r.testSem <- struct{}{}:
		defer func() { <-r.testSem }()
	default:
		return nil, ErrTestBusy
	}
	started := time.Now()
	var rec RunRecord
	if denied := r.checkAccess(ctx, m, steps); denied != "" {
		rec = RunRecord{ScheduleID: m.ID, StartedAt: started.UTC(), FinishedAt: started.UTC(), Result: entity.WatchResultError,
			Error: denied, Reason: clip(denied)}
	} else {
		cwd := ""
		if HasBash(steps) {
			if dir, derr := watchExecDir(r.layout, m); derr == nil {
				cwd = dir
			}
		}
		rec, _ = executeWatch(ctx, m, steps, cwd, watchTmpDir(m), r.connExec)
	}
	rec.Type, rec.Manual, rec.DryRun, rec.StepsRev = entity.ScheduledTypeWatch, true, true, m.StepsRev
	if override != nil {
		rec.StepsRev = 0 // unsaved steps: no revision
	}
	if dir := WatchRunsDir(r.layout, m); dir != "" {
		_ = writeRun(dir, &rec)
	}
	return &rec, nil
}

// runWatch executes one due watch and settles its state:
//
//	matched → on_match stop: deliver ONE message (the caller's message + the
//	          fenced result) and finish (done); on_match continue: deliver
//	          only a match that is new, keep running
//	pending → nothing delivered, nothing written unless the result changed
//	error   → every/cron: retry next tick, one notice after
//	          watchErrorNotifyAfter in a row; an explicit on_fail=done step
//	          or a permission denial fails the schedule and tells the session
//	          once
//	run_at  → one run, its result delivered whatever it is, done
//
// A cadence that runs out (max_runs / ends_at) finishes the schedule and
// tells the session so. Every run goes to the history files.
func (r *Runner) runWatch(ctx context.Context, l zerologLogger, e *watchEntry) {
	ws := r.watches
	ws.mu.Lock()
	m := e.m
	manual := m.ManualFire
	pendingNext := e.pendingNext
	ws.mu.Unlock()

	firedAt := time.Now()
	steps, onMatch, err := ParseWatch(m.Steps)
	if err == nil {
		err = ValidateSteps(steps)
	}
	var rec RunRecord
	var result string
	if err != nil {
		rec = RunRecord{ScheduleID: m.ID, StartedAt: firedAt.UTC(), FinishedAt: firedAt.UTC(), Result: entity.WatchResultError,
			Error: "invalid watch steps: " + err.Error()}
	} else if denied := r.watchAccessOK(ctx, e, steps); denied != "" {
		rec = RunRecord{ScheduleID: m.ID, StartedAt: firedAt.UTC(), FinishedAt: firedAt.UTC(), Result: entity.WatchResultError, Error: denied, Denied: true}
	} else {
		cwd := ""
		if HasBash(steps) {
			if dir, derr := watchExecDir(r.layout, m); derr == nil {
				cwd = dir
			} else {
				l.Warn().Str("id", m.ID).Err(derr).Msg("watch: bash cwd refused")
			}
		}
		rec, result = executeWatch(ctx, m, steps, cwd, watchTmpDir(m), r.connExec)
	}

	prevResult := m.LastResult
	if manual {
		m.ManualRuns++
		rec.Run = m.ManualRuns
	} else {
		m.RunCount++
		rec.Run = m.RunCount
	}
	lastRun := firedAt.UTC()
	m.LastRunAt = &lastRun
	rec.Manual = manual
	rec.Type = entity.ScheduledTypeWatch
	rec.StepsRev = m.StepsRev
	if rec.Reason == "" {
		rec.Reason = clip(rec.Error)
	}
	// What the run means for the watch:
	//   run_at (once)  → notify whatever came out, done
	//   matched        → stop: notify, done; continue: notify only when the
	//                    match is new (matchHash), keep running
	//   error          → every/cron ride it out (a notice after
	//                    watchErrorNotifyAfter in a row, once per error text);
	//                    an explicit on_fail=done step or a permission
	//                    denial (rec.Denied) still fails the watch
	oneShot := m.Kind == entity.ScheduledKindOnce
	statePath := watchStatePath(r.layout, m)
	var st watchState
	stateLoaded, saveState := false, false
	state := func() *watchState {
		if !stateLoaded {
			st, stateLoaded = loadWatchState(statePath), true
		}
		return &st
	}
	if rec.Result == entity.WatchResultError {
		m.ConsecutiveErrors++
	} else if m.ConsecutiveErrors > 0 {
		m.ConsecutiveErrors = 0
		if state().ErrorHash != "" {
			st.ErrorHash, saveState = "", true
		}
	}

	out := WatchOutcome{Result: rec.Result, RunCount: m.RunCount, ManualRuns: m.ManualRuns, LastRunAt: m.LastRunAt}
	if rec.Result == entity.WatchResultError {
		out.LastError = Redact(rec.Error)
	}
	terminal, text := "", ""
	notify := "" // a notice that leaves the watch running
	switch {
	case oneShot && !manual:
		terminal, text = entity.ScheduledStatusDone, watchOnceText(m, rec, result)
	case rec.Result == entity.WatchResultMatched:
		if onMatch != OnMatchContinue || rec.Outcome == OutcomeFail || oneShot {
			terminal, text = entity.ScheduledStatusDone, watchMatchedText(m, rec, result)
		} else if h := matchHash(rec, result); h != state().LastHash {
			st.LastHash, saveState = h, true
			st.Notified++
			notify = watchContinueText(m, rec, result, st.Notified)
		}
	case rec.Result == entity.WatchResultError:
		if oneShot || rec.Denied || explicitFailDone(steps, rec) {
			terminal, text = entity.ScheduledStatusFailed, watchFailedText(m, rec)
		} else if m.ConsecutiveErrors >= watchErrorNotifyAfter {
			if h := hashText(out.LastError); h != state().ErrorHash {
				st.ErrorHash, saveState = h, true
				notify = watchErrorRepeatText(m, rec, m.ConsecutiveErrors)
			}
		}
	}
	if terminal == "" {
		if manual {
			out.Next = pendingNext
		} else if next, aerr := advance(m, firedAt, m.RunCount); aerr == nil {
			out.Next = next
		}
		if out.Next.IsZero() {
			terminal, text = entity.ScheduledStatusDone, watchExhaustedText(m, rec)
			if onMatch == OnMatchContinue && state().Notified > 0 {
				text = watchContinueEndText(m, rec, st.Notified)
			}
			if notify != "" {
				text, notify = notify+"\n\n"+text, ""
			}
		}
	}
	out.ConsecutiveErrors = m.ConsecutiveErrors
	if terminal == "" && (onMatch == OnMatchContinue || rec.Result == entity.WatchResultError) {
		told := notify != ""
		rec.Notified = &told
	}
	if dir := WatchRunsDir(r.layout, m); dir != "" {
		if werr := writeRun(dir, &rec); werr != nil {
			l.Warn().Str("id", m.ID).Err(werr).Msg("watch: write run history failed")
		}
	}

	ws.mu.Lock()
	gone := e.gone
	ws.mu.Unlock()
	if gone {
		// Cancelled or deleted while it ran: say nothing, write nothing.
		return
	}

	if terminal != "" {
		target, serr := r.sendToTarget(ctx, l, m, text)
		if serr != nil {
			r.dropWatch(e) // sendToTarget already failed the row
			return
		}
		out.Status, out.LastSessionID, out.At = terminal, target, stamp()
		if err := r.store.SaveWatchOutcome(ctx, m.ID, m.Kind, out); err != nil {
			l.Warn().Str("id", m.ID).Err(err).Msg("watch: save outcome failed")
		}
		r.dropWatch(e)
		l.Info().Str("id", m.ID).Str("session", target).Str("status", terminal).Str("result", rec.Result).Msg("watch finished")
		return
	}

	if notify != "" {
		if _, serr := r.sendToTarget(ctx, l, m, notify); serr != nil {
			r.dropWatch(e) // sendToTarget already failed the row
			return
		}
	}
	if saveState {
		if err := saveWatchState(statePath, st); err != nil {
			l.Warn().Str("id", m.ID).Err(err).Msg("watch: save state failed")
		}
	}

	// Still live. Persist only what is worth a round trip.
	m.LastResult, m.ConsecutiveErrors, m.LastError, m.ManualFire = out.Result, out.ConsecutiveErrors, out.LastError, false
	write := manual || out.Result != prevResult || notify != ""
	if write {
		out.At = stamp()
		if err := r.store.SaveWatchOutcome(ctx, m.ID, m.Kind, out); err != nil {
			l.Warn().Str("id", m.ID).Err(err).Msg("watch: save outcome failed")
		}
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	// Keep definition edits a refresh made while this ran; take the counters.
	e.m.RunCount, e.m.ManualRuns, e.m.LastRunAt = m.RunCount, m.ManualRuns, m.LastRunAt
	e.m.LastResult, e.m.ConsecutiveErrors, e.m.LastError = m.LastResult, m.ConsecutiveErrors, m.LastError
	if e.m.ManualFire == manual {
		e.m.ManualFire = false
		e.next = out.Next
		e.pendingNext = time.Time{}
	}
	if write {
		e.dbUpdatedAt, e.lastFlush, e.dirty = out.At, time.Now(), false
	} else {
		e.dirty = true
	}
	e.running = false
}

// watchTmpDir makes a run's scratch dir (STEP_<i>_OUT files, the script's
// HOME) in the system temp dir — outside schedules/<id>, so a script cannot
// reach its own run history through a relative path. executeWatch removes
// it; "" when it cannot be made, which fails bash steps.
func watchTmpDir(m entity.ScheduledMessage) string {
	dir, err := os.MkdirTemp("", "wick-watch-"+m.ID+"-")
	if err != nil {
		return ""
	}
	return dir
}

func (r *Runner) dropWatch(e *watchEntry) {
	r.watches.mu.Lock()
	defer r.watches.mu.Unlock()
	e.gone = true
	if cur, ok := r.watches.entries[e.m.ID]; ok && cur == e {
		delete(r.watches.entries, e.m.ID)
	}
}

// stopWatches checkpoints and releases the lease on shutdown.
func (r *Runner) stopWatches(l zerologLogger) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.flushWatches(ctx, l, true)
	_ = r.store.ReleaseLease(ctx, watchLeaseName, r.watches.holder)
}

// WatchLive is a watch's live state as the runner holds it — fresher than
// the row, which is only checkpointed.
type WatchLive struct {
	RunCount          int        `json:"run_count"`
	ManualRuns        int        `json:"manual_runs"`
	LastRunAt         *time.Time `json:"last_run_at,omitempty"`
	LastResult        string     `json:"last_result,omitempty"`
	ConsecutiveErrors int        `json:"consecutive_errors"`
	LastError         string     `json:"last_error,omitempty"`
	NextRunAt         time.Time  `json:"next_run_at"`
	Running           bool       `json:"running"`
}

// LiveWatch reads a watch's live state from the process's runner. false when
// no runner holds it (another process has the lease, or it is not live).
func LiveWatch(id string) (WatchLive, bool) {
	r := running
	if r == nil || r.watches == nil {
		return WatchLive{}, false
	}
	r.watches.mu.Lock()
	defer r.watches.mu.Unlock()
	e, ok := r.watches.entries[id]
	if !ok {
		return WatchLive{}, false
	}
	return WatchLive{RunCount: e.m.RunCount, ManualRuns: e.m.ManualRuns, LastRunAt: e.m.LastRunAt, LastResult: e.m.LastResult,
		ConsecutiveErrors: e.m.ConsecutiveErrors, LastError: e.m.LastError, NextRunAt: e.next, Running: e.running}, true
}

// OverlayLive copies a watch's live counters onto a row read from the DB, so
// every surface reports what the runner knows rather than the last
// checkpoint.
func OverlayLive(m *entity.ScheduledMessage) {
	if !m.IsWatch() {
		return
	}
	m.OnMatch = WatchOnMatch(*m)
	if m.OnMatch == OnMatchContinue {
		m.WatchNotified = WatchNotified(*m)
	}
	if !m.IsLive() {
		return
	}
	lv, ok := LiveWatch(m.ID)
	if !ok {
		return
	}
	m.RunCount, m.ManualRuns, m.LastRunAt = lv.RunCount, lv.ManualRuns, lv.LastRunAt
	m.LastResult, m.ConsecutiveErrors, m.LastError = lv.LastResult, lv.ConsecutiveErrors, lv.LastError
	if !lv.NextRunAt.IsZero() && !m.ManualFire {
		m.RunAt = lv.NextRunAt
	}
}

// sweepInterval is how often the lease holder clears old run history.
const sweepInterval = time.Hour

// maybeSweep removes, at most once per sweepInterval, the history folders of
// schedules finished over 30 days ago and of schedules that no longer exist.
// Only the lease holder sweeps, so two processes never race on it.
func (r *Runner) maybeSweep(ctx context.Context, l zerologLogger) {
	r.watches.mu.Lock()
	if time.Since(r.watches.lastSweep) < sweepInterval {
		r.watches.mu.Unlock()
		return
	}
	r.watches.lastSweep = time.Now()
	r.watches.mu.Unlock()
	keep, err := r.store.ScheduleIDsToKeep(ctx, time.Now().Add(-sweepTerminalAge))
	if err != nil {
		l.Warn().Err(err).Msg("schedule history sweep: list failed")
		return
	}
	if n := sweepScheduleDirs(r.layout, keep); n > 0 {
		l.Info().Int("removed", n).Msg("schedule history sweep")
	}
}

// UserLookup resolves a user and their filter tags (nil user = gone).
type UserLookup func(ctx context.Context, userID string) (*entity.User, []string, error)

// ConnectorCall runs one connector op as user (with tagIDs) for sessionID.
type ConnectorCall func(ctx context.Context, toolID string, params map[string]any, sessionID string, user *entity.User, tagIDs []string) (string, error)

// NewConnectorExecutor builds the watch connector executor. Every call runs
// as the schedule's OWN identity — RunAsUserID when an admin set one, else
// OwnerUserID — resolved fresh (user, approval, tags) on each run, so the op
// sees exactly that user's connectors, tags and accounts (an @account in the
// tool_id included). No owner, a missing or unapproved user, or a failed
// lookup is an error: there is no fallback to a broader identity.
func NewConnectorExecutor(lookup UserLookup, call ConnectorCall) ConnectorExecutor {
	return func(ctx context.Context, m entity.ScheduledMessage, toolID string, params map[string]any) (string, error) {
		runAs := m.EffectiveRunAsUser()
		if runAs == "" {
			return "", watchDenied("watch has no owner or run-as user to run connector steps as")
		}
		u, tags, err := lookup(ctx, runAs)
		if err != nil {
			// A lookup that failed says nothing about the user: retry.
			return "", errors.New("run-as user " + runAs + " lookup failed: " + err.Error())
		}
		if u == nil || !u.Approved {
			return "", watchDenied("run-as user " + runAs + " is missing or not approved")
		}
		sessionID := m.SessionID
		if sessionID == "" {
			sessionID = m.SourceSessionID
		}
		return call(ctx, toolID, params, sessionID, u, tags)
	}
}

// watchDeniedError is a permission refusal (no identity, user gone or
// unapproved): unlike a transient error it ends the watch.
type watchDeniedError struct{ msg string }

func (e watchDeniedError) Error() string { return e.msg }

func watchDenied(msg string) error { return watchDeniedError{msg: msg} }

func isWatchDenied(err error) bool {
	var d watchDeniedError
	return errors.As(err, &d)
}

// explicitFailDone reports whether the step an error stopped on set
// on_fail=done itself — the one way an error still finishes an every/cron
// watch (the default there is to retry next tick).
func explicitFailDone(steps []Step, rec RunRecord) bool {
	n := len(rec.Steps)
	if n == 0 || n > len(steps) || rec.Steps[n-1].Decision != DecisionFail {
		return false
	}
	return steps[n-1].OnFail == OnFailDone
}
