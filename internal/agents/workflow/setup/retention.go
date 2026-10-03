package setup

import (
	"context"
	"path/filepath"
	"sort"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/service"
	"github.com/yogasw/wick/internal/agents/workflow/state"
)

// Run-retention defaults. A run's state.json can carry megabytes of node
// output, so history is capped both by count and by age.
const (
	DefaultRunKeepMax       = 50
	DefaultRunRetentionDays = 7
	// RunCleanupInterval is how often the background pass sweeps every
	// workflow, on top of the per-workflow pass after each finished run.
	RunCleanupInterval = 6 * time.Hour
)

// CleanupOptions tunes the run-retention pass.
type CleanupOptions struct {
	// KeepMax is the hard cap of finished runs kept per workflow; the
	// newest KeepMax survive (unless older than TTL), the rest go.
	// <= 0 = DefaultRunKeepMax.
	KeepMax int
	// TTL removes a finished run whose EndedAt is older than this, even
	// inside KeepMax. <= 0 = DefaultRunRetentionDays.
	TTL time.Duration
	Now func() time.Time
}

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
	for i, r := range done {
		if i < opts.KeepMax && now.Sub(r.ended) <= opts.TTL {
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

// isTerminal reports a status the engine never moves on from.
func isTerminal(status string) bool {
	return status == workflow.StatusSuccess || status == workflow.StatusFailed
}

// StartRunRetention runs one sweep shortly after boot, then every
// RunCleanupInterval, until ctx is done; it also prunes a workflow right
// after one of its runs finishes. opts is re-read on every pass so a
// config change lands without a restart.
func (m *Manager) StartRunRetention(ctx context.Context, opts func() CleanupOptions) {
	if opts == nil {
		opts = func() CleanupOptions { return CleanupOptions{} }
	}
	m.Engine.AfterRun = func(id string) {
		if n, err := CleanupWorkflowRuns(m.Layout, id, opts()); err == nil && n > 0 {
			log.Info().Str("component", "wf").Str("wf_id", id).Int("removed", n).Msg("workflow run retention")
		}
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
