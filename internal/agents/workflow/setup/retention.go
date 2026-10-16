package setup

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/service"
	"github.com/yogasw/wick/internal/agents/workflow/state"
)

// Run-retention defaults. A run's state.json can carry megabytes of node
// output, so history is capped both by count and by age.
const (
	DefaultRunKeepMax       = 50
	DefaultRunRetentionDays = 1
	// RunCleanupInterval is how often the background pass sweeps every
	// workflow, on top of the per-workflow pass after each finished run.
	RunCleanupInterval = 6 * time.Hour
)

// CleanupOptions tunes the run-retention pass.
type CleanupOptions struct {
	// KeepMax is how many finished runs survive a trim. The trim itself only
	// fires once the history reaches 2 x KeepMax (see PruneAt); the newest
	// KeepMax stay (unless older than TTL), the rest go.
	// <= 0 = DefaultRunKeepMax.
	KeepMax int
	// TTL removes a finished run whose EndedAt is older than this, even
	// inside KeepMax. <= 0 = DefaultRunRetentionDays.
	TTL time.Duration
	Now func() time.Time
}

// PruneAt is the number of finished runs at which the count cap kicks in
// and trims the history back down to KeepMax.
func (o CleanupOptions) PruneAt() int { return o.KeepMax * 2 }

func (o CleanupOptions) withDefaults() CleanupOptions {
	if o.KeepMax <= 0 {
		o.KeepMax = DefaultRunKeepMax
	}
	if o.TTL <= 0 {
		o.TTL = DefaultRunRetentionDays * 24 * time.Hour
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// CleanupRuns applies the retention policy to every workflow's runs/.
// Best-effort per workflow: one unreadable folder doesn't stop the rest.
func CleanupRuns(layout config.Layout, opts CleanupOptions) (removed int, err error) {
	ids, err := service.New(layout).List()
	if err != nil {
		return 0, err
	}
	store := state.New(layout)
	for _, id := range ids {
		n, _ := cleanupWorkflowRuns(layout, store, id, opts)
		removed += n
	}
	return removed, nil
}

// CleanupWorkflowRuns applies the retention policy to one workflow.
func CleanupWorkflowRuns(layout config.Layout, id string, opts CleanupOptions) (int, error) {
	return cleanupWorkflowRuns(layout, state.New(layout), id, opts)
}

// retainedRun is one finished run as the retention pass sees it.
type retainedRun struct {
	id      string
	started time.Time
	ended   time.Time
}

// cleanupWorkflowRuns keeps at most KeepMax finished runs, newest by
// StartedAt, and drops any finished run older than TTL. A run that has
// not ended (running / queued / paused, EndedAt nil) is never touched
// and never counts toward the cap. A run folder with no readable
// state is left alone too — retention only deletes what it can prove
// finished.
//
// Run ids are UUIDs, so the folder name says nothing about age: the
// order comes from the index rows (written once, at run end) with
// state.json as the fallback for runs the index lacks. Index rows whose
// folder is gone are dropped in the same pass, so the Runs panel never
// lists a ghost.
func cleanupWorkflowRuns(layout config.Layout, store *state.FileStore, id string, opts CleanupOptions) (int, error) {
	opts = opts.withDefaults()
	names, err := store.ListRuns(id)
	if err != nil {
		return 0, err
	}
	indexName := filepath.Base(layout.WorkflowIndexDir(id))
	rows, _ := store.IndexAll(id)
	byID := make(map[string]state.IndexEntry, len(rows))
	for _, r := range rows {
		if _, seen := byID[r.ID]; !seen { // newest first: keep the latest row
			byID[r.ID] = r
		}
	}

	present := make(map[string]bool, len(names))
	var done []retainedRun
	for _, name := range names {
		if name == indexName {
			continue
		}
		present[name] = true
		if r, ok := byID[name]; ok && r.EndedAt != nil && isTerminal(r.Status) {
			done = append(done, retainedRun{id: name, started: r.StartedAt, ended: *r.EndedAt})
			continue
		}
		st, err := store.Load(id, name)
		if err != nil || st.EndedAt == nil || !isTerminal(st.Status) {
			continue
		}
		done = append(done, retainedRun{id: name, started: st.StartedAt, ended: *st.EndedAt})
	}

	sort.SliceStable(done, func(i, j int) bool { return done[i].started.After(done[j].started) })
	now := opts.Now()
	removed := 0
	gone := map[string]bool{}
	// Count cap with hysteresis: nothing is trimmed until the finished runs
	// reach PruneAt (2 x KeepMax), then the oldest go until KeepMax remain.
	// Trimming one run per finished run would rewrite the index every time;
	// this does it once per KeepMax runs. The TTL still applies on every pass.
	trim := len(done) >= opts.PruneAt()
	for i, r := range done {
		if !(trim && i >= opts.KeepMax) && now.Sub(r.ended) <= opts.TTL {
			continue
		}
		if err := removeAll(layout.WorkflowRunDir(id, r.id)); err != nil {
			continue
		}
		gone[r.id] = true
		removed++
	}

	ghost := false
	for rid := range byID {
		if !present[rid] {
			ghost = true
			break
		}
	}
	if removed > 0 || ghost {
		if _, err := store.IndexRemove(id, func(e state.IndexEntry) bool {
			return gone[e.ID] || !present[e.ID]
		}); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// adhocSessionPrefix marks the one-off sessions a `session: new` node mints
// for a single workflow run. session_init creates the folder up front, before
// anything decides whether an agent turn will run, so a busy cron leaves a
// fresh empty session behind on every tick — hundreds a day, none ever reused.
const adhocSessionPrefix = "wf_adhoc_"

// CleanupAdhocSessions deletes idle wf_adhoc_ sessions that nobody has touched
// for longer than the run-retention TTL. A session that is queued or running is
// never removed. Best-effort: one unreadable session does not stop the rest.
func CleanupAdhocSessions(layout config.Layout, opts CleanupOptions) (int, error) {
	opts = opts.withDefaults()
	ids, err := session.List(layout)
	if err != nil {
		return 0, err
	}
	now := opts.Now()
	removed := 0
	for _, id := range ids {
		if !strings.HasPrefix(id, adhocSessionPrefix) {
			continue
		}
		sess, err := session.Load(layout, id)
		if err != nil || sess.Meta.Status != session.StatusIdle {
			continue
		}
		if now.Sub(sess.Meta.LastActive) <= opts.TTL {
			continue
		}
		if err := session.Delete(context.Background(), layout, id); err != nil {
			continue
		}
		removed++
	}
	return removed, nil
}

// isTerminal reports a status the engine never moves on from.
func isTerminal(status string) bool {
	return status == workflow.StatusSuccess || status == workflow.StatusFailed
}

// StartRunRetention wires the per-workflow pass that prunes a workflow
// right after one of its runs finishes, and remembers opts for the
// background sweep. It does NOT sweep: during a graceful upgrade the
// predecessor still owns intake and is still finishing runs, so the
// all-workflow pass waits for StartRunSweep, called once this process
// holds the intake baton. opts is re-read on every pass so a config
// change lands without a restart.
func (m *Manager) StartRunRetention(opts func() CleanupOptions) {
	if opts == nil {
		opts = func() CleanupOptions { return CleanupOptions{} }
	}
	m.retentionOpts = opts
	m.Engine.AfterRun = func(id string) {
		if n, err := CleanupWorkflowRuns(m.Layout, id, opts()); err == nil && n > 0 {
			log.Info().Str("component", "wf").Str("wf_id", id).Int("removed", n).Msg("workflow run retention")
		}
	}
}

// StartRunSweep runs one all-workflow sweep now, then every
// RunCleanupInterval, until ctx is done. Call it only after this process
// has taken over intake (next to StartCron).
func (m *Manager) StartRunSweep(ctx context.Context) {
	opts := m.retentionOpts
	if opts == nil {
		opts = func() CleanupOptions { return CleanupOptions{} }
	}
	go func() {
		sweep := func() {
			n, err := CleanupRuns(m.Layout, opts())
			if err != nil {
				log.Warn().Err(err).Str("component", "wf").Msg("workflow run retention failed")
				return
			}
			if n > 0 {
				log.Info().Str("component", "wf").Int("removed", n).Msg("workflow run retention")
			}
			if n, err := CleanupAdhocSessions(m.Layout, opts()); err == nil && n > 0 {
				log.Info().Str("component", "wf").Int("removed", n).Msg("workflow adhoc session retention")
			}
		}
		sweep()
		t := time.NewTicker(RunCleanupInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sweep()
			}
		}
	}()
}
