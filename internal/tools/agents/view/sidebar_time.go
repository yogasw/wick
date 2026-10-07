package view

import (
	"strconv"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
)

// RelativeAge is the compact "last update" label on a sidebar row: "now",
// "3m", "2h", "5d". Deliberately terse — the row is 15rem wide and the
// title is the thing worth reading; the age only has to answer "is this
// the one I touched a minute ago or last week".
//
// MIRRORED in fe/agents/shell/src/sidebarOrder.ts (relativeAge) — the
// island refreshes this label every 30s. Change both together.
func RelativeAge(lastMs, nowMs int64) string {
	if lastMs <= 0 {
		return ""
	}
	d := nowMs - lastMs
	switch {
	case d < 60_000:
		return "now"
	case d < 3_600_000:
		return strconv.FormatInt(d/60_000, 10) + "m"
	case d < 86_400_000:
		return strconv.FormatInt(d/3_600_000, 10) + "h"
	default:
		return strconv.FormatInt(d/86_400_000, 10) + "d"
	}
}

// IsRunningStatus reports whether an effectiveStatus value means "work is
// happening or about to": those rows sort to the top of the sidebar and
// carry the accent border. Idle is a warm process with nothing to do, so
// it sorts by age like everything else.
//
// MIRRORED in fe/agents/shell/src/sidebarOrder.ts (isRunningStatus).
func IsRunningStatus(status string) bool {
	switch status {
	case "working", "spawning", "subagent", "queued":
		return true
	}
	return false
}

// SidebarRowStatus is the status a sidebar row renders, from the same
// inputs the dot uses.
func SidebarRowStatus(sess session.Session, lc SessionLifecycleVM) string {
	return effectiveStatus(lc.Lifecycle, string(sess.Meta.Status), lc.SubAgent)
}

// SidebarLastActiveMs is the newest activity we know of for a row: the
// session's persisted LastActive or the pool's in-memory one, whichever
// is later. The pool's moves on every turn event, the meta's only when it
// is saved, so taking the max keeps the label honest mid-turn.
func SidebarLastActiveMs(sess session.Session, lc SessionLifecycleVM) int64 {
	var ms int64
	if !sess.Meta.LastActive.IsZero() {
		ms = sess.Meta.LastActive.UnixMilli()
	}
	if lc.LastActiveMs > ms {
		ms = lc.LastActiveMs
	}
	return ms
}

func sidebarAgeLabel(lastMs int64) string {
	return RelativeAge(lastMs, time.Now().UnixMilli())
}
