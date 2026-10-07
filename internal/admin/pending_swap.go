package admin

import (
	"time"

	"github.com/yogasw/wick/internal/pkg/daemon"
)

// pendingSwap is the JSON shape the System page and its poll consume. The
// detection itself lives in internal/pkg/daemon, because the auto-swap
// watcher in the server needs exactly the same answer and the two must never
// drift apart.
type pendingSwap struct {
	Pending bool   `json:"pending"`
	From    string `json:"from,omitempty"`   // version running now
	To      string `json:"to,omitempty"`     // version waiting on disk / staged
	Source  string `json:"source,omitempty"` // where the new build came from
	Built   string `json:"built,omitempty"`  // build timestamp of the waiting binary
	// InstalledAgo is how long the file has been sitting there, in seconds.
	// "Built at 14:32Z" does not answer "has this been waiting all morning?".
	InstalledAgo int `json:"installed_ago,omitempty"`
	// Blocked is why this build will never be applied on its own, empty when
	// it is simply waiting its turn. Shown instead of a countdown: a card that
	// keeps promising a swap that cannot happen is worse than no card.
	Blocked string `json:"blocked,omitempty"`
	// Want is the version that would be accepted.
	Want string `json:"want,omitempty"`
}

func detectPendingSwap(runningVersion, runningBuiltAt string) pendingSwap {
	p, ok := daemon.PendingSwap(runningVersion, runningBuiltAt)
	if !ok {
		return pendingSwap{}
	}
	ago := 0
	if p.ModTime > 0 {
		if d := int(time.Since(time.Unix(p.ModTime, 0)).Seconds()); d > 0 {
			ago = d
		}
	}
	return pendingSwap{
		Pending: true, From: runningVersion, To: p.Version,
		Source: p.Path, Built: p.Built, InstalledAgo: ago,
		Blocked: p.Blocked, Want: p.Want,
	}
}
