package agents

import (
	"sort"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/tools/agents/view"
)

// orderSidebarIDs puts running rows first (working / spawning / a working
// sub-agent / queued), then everything else by last use, newest first,
// then by id.
//
// It runs BEFORE the sidebar cap on purpose: "which one is running" is the
// question the sidebar is scanned for, and a busy session that happened to
// be touched eleventh would otherwise be cut off the list entirely.
//
// The registry already hands ids over LastActive-descending, but only by
// the persisted meta; the pool's in-memory LastActive is fresher mid-turn,
// so the age is recomputed from both. MIRRORED client-side by
// fe/agents/shell/src/sidebarOrder.ts (sortRows) for live re-sorts.
func orderSidebarIDs(ids []string, sessions map[string]session.Session, lc map[string]view.SessionLifecycleVM) []string {
	type row struct {
		id      string
		running bool
		at      int64
	}
	rows := make([]row, 0, len(ids))
	for _, id := range ids {
		sess := sessions[id]
		l := lc[id]
		rows = append(rows, row{
			id:      id,
			running: view.IsRunningStatus(view.SidebarRowStatus(sess, l)),
			at:      view.SidebarLastActiveMs(sess, l),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].running != rows[j].running {
			return rows[i].running
		}
		if rows[i].at != rows[j].at {
			return rows[i].at > rows[j].at
		}
		// The id breaks a tie so the order is total: the untracked rail
		// pages by this key (railKey), and "after this row" needs every
		// row to have exactly one place.
		return rows[i].id < rows[j].id
	})
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.id
	}
	return out
}

// sidebarLifecycles is the pool's view of every live session, keyed the
// way the sidebar reads it: lifecycle, pid, the in-memory LastActive, and
// any working sub-agent folded into the conversation that owns it.
//
// Every list that shows a chat's age or sorts by it builds from this, so
// the sidebar, the ticket board's untracked rail and a ticket's own chat
// list can never disagree about which chat was touched last — the board
// used to read the persisted meta alone and lagged a running turn by
// however long it had been since the last save.
func sidebarLifecycles() map[string]view.SessionLifecycleVM {
	lc := make(map[string]view.SessionLifecycleVM)
	if globalPool == nil {
		return lc
	}
	liveBySession := make(map[string]string)
	for _, e := range globalPool.ActiveSnapshot() {
		entry := view.SessionLifecycleVM{Lifecycle: e.Lifecycle, PID: e.PID}
		if !e.LastActive.IsZero() {
			entry.LastActiveMs = e.LastActive.UnixMilli()
		}
		lc[e.SessionID] = entry
		liveBySession[e.SessionID] = e.Lifecycle
	}
	// Sub-agents run under their own session ids, which have no sidebar row
	// of their own, so their liveness is folded into the conversation that
	// owns them. Without this a row goes dark as soon as the leader idles,
	// even while its children are still working.
	for root, sub := range rollUpSubAgentWork(liveBySession, sessionParentOf) {
		entry := lc[root]
		entry.SubAgent = sub
		lc[root] = entry
	}
	return lc
}
