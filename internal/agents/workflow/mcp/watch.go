// Package mcp — workflow_watch implementation.
//
// Watch is deliberately bounded:
//
//   - Reads come from the sharded run index. Per-run state.Load only
//     happens when a filter needs node_id / trigger_id, and even then
//     it stops the moment `limit` results are collected.
//
//   - wait_seconds is an UPPER bound. The handler subscribes to
//     Engine's event broker, returns the instant the target count is
//     met, and otherwise waits the remaining time.
//
//   - All inputs are caps (limit ≤ 50, wait_seconds ≤ 30). AI can
//     never trigger a long-running scan by accident.
//
// See doc 25 for the full design notes.
package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
)

// WatchInput is the workflow_watch request shape.
type WatchInput struct {
	WorkflowID  string `json:"workflow_id,omitempty"`
	TriggerID   string `json:"trigger_id,omitempty"`
	NodeID      string `json:"node_id,omitempty"`
	Status      string `json:"status,omitempty"` // any | success | failed | running
	Since       string `json:"since,omitempty"`  // RFC3339 absolute or "-15m" relative
	Limit       int    `json:"limit,omitempty"`
	WaitSeconds int    `json:"wait_seconds,omitempty"`
	Expect      int    `json:"expect,omitempty"`
	StopOnFirst bool   `json:"stop_on_first,omitempty"`
}

// WatchRow is one returned run summary. Deliberately tiny —
// downstream calls workflow_get_run_log per id when it wants more.
type WatchRow struct {
	RunID      string     `json:"run_id"`
	WorkflowID string     `json:"workflow_id"`
	Status     string     `json:"status,omitempty"`
	TriggerID  string     `json:"trigger_id,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
}

// WatchResult is the response.
type WatchResult struct {
	Runs         []WatchRow `json:"runs"`
	CheckedUntil time.Time  `json:"checked_until"`
	Truncated    bool       `json:"truncated,omitempty"`
}

// Hard caps. Tool inputs above these are silently clamped.
const (
	watchMaxLimit       = 50
	watchMaxWaitSeconds = 30
	watchDefaultLimit   = 10
)

// Watch resolves runs matching the filter, optionally subscribing to
// the live engine broker for wait_seconds.
func (m *Ops) Watch(ctx context.Context, in WatchInput) (WatchResult, error) {
	if m.StateStore == nil {
		return WatchResult{}, fmt.Errorf("state store not configured")
	}
	in = normalizeWatchInput(in)
	since, err := parseSince(in.Since)
	if err != nil {
		return WatchResult{}, fmt.Errorf("since: %w", err)
	}
	target := watchTarget(in)

	// Phase 1 — peek the index for already-landed runs since `since`.
	collected, err := m.watchPeek(in, since, target)
	if err != nil {
		return WatchResult{}, err
	}
	checkedUntil := time.Now().UTC()

	// Phase 2 — when peek already satisfies, return. Same when caller
	// asked for non-blocking (wait_seconds = 0).
	if len(collected) >= target || in.WaitSeconds == 0 || m.Engine == nil {
		truncated := len(collected) > in.Limit
		if truncated {
			collected = collected[:in.Limit]
		}
		return WatchResult{Runs: collected, CheckedUntil: checkedUntil, Truncated: truncated}, nil
	}

	// Phase 3 — subscribe and wait. Returns the moment `target` is
	// satisfied OR wait_seconds elapses OR ctx is cancelled.
	deadline := time.Now().Add(time.Duration(in.WaitSeconds) * time.Second)
	sub := m.Engine.Subscribe(64)
	defer sub.Cancel()

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		select {
		case sev, ok := <-sub.Ch:
			if !ok {
				return WatchResult{Runs: collected, CheckedUntil: time.Now().UTC()}, nil
			}
			// We listen only on terminal events to avoid noise from
			// per-node progress. Terminal = the engine fires a workflow
			// completion / failure event when a run ends.
			if sev.Event.Event != workflow.EventWorkflowCompleted &&
				sev.Event.Event != workflow.EventWorkflowFailed {
				continue
			}
			// Workflow-id scope filter is cheap before loading state.
			if in.WorkflowID != "" && sev.WorkflowID != in.WorkflowID {
				continue
			}
			row, ok := m.materialiseRow(sev.WorkflowID, sev.RunID)
			if !ok {
				continue
			}
			if !rowMatchesFilter(row, in, sev.RunID) {
				continue
			}
			// Dedup: live stream may overlap peek when a run lands
			// between phase 1 and phase 3. Skip if we already have it.
			if containsRunID(collected, row.RunID) {
				continue
			}
			collected = append(collected, row)
			if len(collected) >= target {
				truncated := len(collected) > in.Limit
				if truncated {
					collected = collected[:in.Limit]
				}
				return WatchResult{Runs: collected, CheckedUntil: time.Now().UTC(), Truncated: truncated}, nil
			}
		case <-time.After(remaining):
			// fall through to return what we have
		case <-ctx.Done():
			return WatchResult{Runs: collected, CheckedUntil: time.Now().UTC()}, ctx.Err()
		}
		if !time.Now().Before(deadline) {
			break
		}
	}
	truncated := len(collected) > in.Limit
	if truncated {
		collected = collected[:in.Limit]
	}
	return WatchResult{Runs: collected, CheckedUntil: time.Now().UTC(), Truncated: truncated}, nil
}

// watchPeek scans the sharded index for already-landed runs that
// match the filter. Pulls in pages of `pageSize` until target met,
// limit hit, or older than `since`.
func (m *Ops) watchPeek(in WatchInput, since time.Time, target int) ([]WatchRow, error) {
	out := []WatchRow{}
	workflowIDs, err := m.watchScope(in)
	if err != nil {
		return nil, err
	}
	for _, wfID := range workflowIDs {
		page := 0
		for {
			const pageSize = 50
			entries, more, err := m.StateStore.IndexList(wfID, page, pageSize)
			if err != nil {
				return out, err
			}
			stop := false
			for _, e := range entries {
				if !e.StartedAt.After(since) {
					stop = true
					break
				}
				row := WatchRow{
					RunID:      e.ID,
					WorkflowID: wfID,
					Status:     e.Status,
					StartedAt:  e.StartedAt,
					EndedAt:    e.EndedAt,
				}
				if !rowMatchesFilter(row, in, e.ID) {
					continue
				}
				// Enrich trigger_id from RunState only when the filter
				// asks for it or when a richer surface is useful. Keep
				// cheap: skip otherwise.
				if in.TriggerID != "" || in.NodeID != "" {
					if !m.runStateMatches(wfID, e.ID, in, &row) {
						continue
					}
				}
				out = append(out, row)
				if len(out) >= target {
					return out, nil
				}
			}
			if stop || !more {
				break
			}
			page++
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// watchScope returns the workflow ids to scan. Single id when the
// caller passed workflow_id; otherwise every workflow Service knows
// about. Sorted for deterministic test output.
func (m *Ops) watchScope(in WatchInput) ([]string, error) {
	if in.WorkflowID != "" {
		return []string{in.WorkflowID}, nil
	}
	if m.Service == nil {
		return nil, nil
	}
	ids, err := m.Service.List()
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}

// runStateMatches loads the RunState only when the filter needs it
// (trigger_id / node_id). When the match holds, the row is enriched
// in place with the resolved trigger_id.
func (m *Ops) runStateMatches(wfID, runID string, in WatchInput, row *WatchRow) bool {
	st, err := m.StateStore.Load(wfID, runID)
	if err != nil {
		return false
	}
	if in.TriggerID != "" {
		if st.Event.TriggerID == "" || st.Event.TriggerID != in.TriggerID {
			return false
		}
	}
	if in.NodeID != "" {
		if !nodeTouched(st, in.NodeID) {
			return false
		}
	}
	row.TriggerID = st.Event.TriggerID
	return true
}

// nodeTouched reports whether the run reached, finished, or skipped
// the given node id.
func nodeTouched(st workflow.RunState, nodeID string) bool {
	for _, n := range st.Completed {
		if n == nodeID {
			return true
		}
	}
	for _, n := range st.Failed {
		if n == nodeID {
			return true
		}
	}
	for _, n := range st.Skipped {
		if n == nodeID {
			return true
		}
	}
	for _, n := range st.Current {
		if n == nodeID {
			return true
		}
	}
	return false
}

// materialiseRow turns a broker event into the WatchRow shape by
// pulling the freshly-saved RunState. Used in the long-poll path.
func (m *Ops) materialiseRow(wfID, runID string) (WatchRow, bool) {
	st, err := m.StateStore.Load(wfID, runID)
	if err != nil {
		return WatchRow{}, false
	}
	row := WatchRow{
		RunID:      runID,
		WorkflowID: wfID,
		Status:     st.Status,
		StartedAt:  st.StartedAt,
		EndedAt:    st.EndedAt,
		TriggerID:  st.Event.TriggerID,
	}
	return row, true
}

// rowMatchesFilter checks the cheap (index-only) filters. trigger_id /
// node_id need state.Load and are handled separately.
func rowMatchesFilter(row WatchRow, in WatchInput, _ string) bool {
	if in.WorkflowID != "" && row.WorkflowID != in.WorkflowID {
		return false
	}
	if in.Status != "" && in.Status != "any" && row.Status != in.Status {
		return false
	}
	return true
}

// containsRunID is the dedup helper for the long-poll path.
func containsRunID(rows []WatchRow, runID string) bool {
	for _, r := range rows {
		if r.RunID == runID {
			return true
		}
	}
	return false
}

// normalizeWatchInput applies caps + defaults so the rest of the
// handler can read the input verbatim.
func normalizeWatchInput(in WatchInput) WatchInput {
	if in.Limit <= 0 {
		in.Limit = watchDefaultLimit
	}
	if in.Limit > watchMaxLimit {
		in.Limit = watchMaxLimit
	}
	if in.WaitSeconds < 0 {
		in.WaitSeconds = 0
	}
	if in.WaitSeconds > watchMaxWaitSeconds {
		in.WaitSeconds = watchMaxWaitSeconds
	}
	if in.Expect < 0 {
		in.Expect = 0
	}
	if in.StopOnFirst && in.Expect == 0 {
		in.Expect = 1
	}
	if in.Status == "" {
		in.Status = "any"
	}
	return in
}

// watchTarget is the count at which the handler returns early. Defaults
// to Limit so a peek call returns whatever is there up to the cap.
func watchTarget(in WatchInput) int {
	if in.Expect > 0 {
		if in.Expect > in.Limit {
			return in.Limit
		}
		return in.Expect
	}
	return in.Limit
}

// parseSince accepts RFC3339 absolute timestamps and a "-<duration>"
// relative form ("-15m", "-1h"). Defaults to "now" when empty.
// Relative is clamped to -24h to keep scans cheap.
func parseSince(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Now().UTC(), nil
	}
	if strings.HasPrefix(s, "-") {
		d, err := time.ParseDuration(s[1:])
		if err != nil {
			return time.Time{}, err
		}
		const maxLookback = 24 * time.Hour
		if d > maxLookback {
			d = maxLookback
		}
		return time.Now().UTC().Add(-d), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// Engine bus method symbol — referenced so build catches drift if
// the broker is removed accidentally.
var _ = (*engine.Engine).Subscribe
