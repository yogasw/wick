package upgrade

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/yogasw/wick/internal/processctl"
)

// channels_handover.go moves channel intake (Slack sockets, Telegram
// polling) to the successor as soon as a drain begins, rather than when the
// draining process exits.
//
// Holding it to the end meant every new message kept landing on the OLD
// build: it started new turns there, so a busy host never settled, and the
// fix just deployed answered nobody until the drain happened to find a lull.
//
// The handshake is two files in the data dir, both ignored by a binary that
// predates them (which then keeps the old rule: channels move with the
// intake baton, when the draining process exits):
//
//  1. The draining process sets ChannelsReleased in draining.json.
//  2. The successor sees it, starts its channels, and writes channels.taken
//     with its pid.
//  3. The draining process sees that and stops its own listeners. Until
//     then both listen for a moment: an event lands on one of them, never
//     on neither. It keeps sending — a reply still being written goes out.
//
// Cron, workflows and scheduled messages still move with the intake baton:
// two processes running them would double-fire.
const channelsTakenFile = "channels.taken"

var channelsReleased atomic.Bool

// ReleaseChannels records that this draining process has given its channel
// intake away: its drain record says so, and no channel (re)starts here.
func ReleaseChannels() { channelsReleased.Store(true) }

// ChannelsReleased reports whether this process gave its channel intake away.
func ChannelsReleased() bool { return channelsReleased.Load() }

// PredecessorReleasedChannels reports whether a still-draining previous
// process has offered its channel intake to this one.
func PredecessorReleasedChannels(dir string) bool {
	st, ok := ReadDrainState(dir)
	return ok && st.PID != os.Getpid() && st.ChannelsReleased
}

func channelsTakenPath(dir string) string { return filepath.Join(dir, channelsTakenFile) }

// MarkChannelsTaken records that this process now runs the channels.
func MarkChannelsTaken(dir string) {
	if dir == "" {
		return
	}
	//nolint:errcheck // best-effort: without it the predecessor keeps listening until it exits
	_ = os.WriteFile(channelsTakenPath(dir), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

// ChannelsTakenByOther reports whether another living process has started
// the channels — the successor answering ReleaseChannels. A record naming
// this process, or one that is gone, is left over from an earlier handover.
func ChannelsTakenByOther(dir string) bool {
	if dir == "" {
		return false
	}
	b, err := os.ReadFile(channelsTakenPath(dir))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 || pid == os.Getpid() {
		return false
	}
	return processctl.ProcessAlive(pid)
}
