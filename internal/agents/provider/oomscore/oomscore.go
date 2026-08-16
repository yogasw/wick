// Package oomscore biases the kernel OOM killer's victim selection.
//
// The kernel picks a victim by badness score, and wick — holding session
// state and buffers — is a fat target sitting next to the agents it
// spawned. Writing a high oom_score_adj on each agent and a low one on
// wick itself tells the kernel which of them the operator would rather
// lose. It kills nothing on its own; it only orders the queue.
//
// This is the one layer of the memory guard that needs neither systemd
// nor cgroup delegation — just a file write — so it is also the only one
// that works on Termux/Android, where lmkd reads the very same knob.
package oomscore

import "fmt"

// Score bounds accepted by the kernel for oom_score_adj.
const (
	minScore = -1000
	maxScore = 1000
)

// AgentScore biases an agent subprocess toward being chosen first.
// DaemonScore biases wick itself away from selection.
const (
	AgentScore  = 800
	DaemonScore = -500
)

// validate rejects scores the kernel would refuse, so a caller bug
// surfaces here rather than as an opaque write error.
func validate(score int) error {
	if score < minScore || score > maxScore {
		return fmt.Errorf("oom score %d out of range [%d,%d]", score, minScore, maxScore)
	}
	return nil
}

// AdjustSelf biases the calling process (wick) away from OOM selection.
func AdjustSelf(score int) error { return Adjust(selfPid(), score) }
