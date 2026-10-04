package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// NoteRechecked labels a reply read back by "Check again" after its turn
// had timed out or ended without its marker.
const NoteRechecked = "rechecked"

// Recheck is the result of reading a turn's reply again.
type Recheck struct {
	// Text is the remote's reply as it stands now.
	Text string `json:"text"`
	// Busy: the remote still shows it works (a progress note, a status);
	// Done: its end marker or a final status is there.
	Busy bool `json:"busy"`
	Done bool `json:"done"`
	// Label is its progress note, if any.
	Label string `json:"label,omitempty"`
}

// Settled reports whether r is a reply worth keeping: finished, or text
// the remote no longer works on.
func (r Recheck) Settled() bool {
	return r.Done || (strings.TrimSpace(r.Text) != "" && !r.Busy)
}

// Rechecker is optionally implemented by a Source: it reads the reply to
// the session's last turn again — for a turn that timed out or ended
// without its marker — sending nothing to the remote. A Source without it
// offers no "Check again".
type Rechecker interface {
	Recheck(ctx context.Context, sessionDir string) (Recheck, error)
}

// ErrNoTurn: the session has posted no turn to read back.
var ErrNoTurn = errors.New("this session has no remote turn to check")

// timeoutPrefix opens TimeoutMessage.
const timeoutPrefix = "No reply from the remote agent after "

// IsTimeout reports whether msg is a turn's TimeoutMessage, and how long
// the turn waited.
func IsTimeout(msg string) (time.Duration, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(msg), timeoutPrefix)
	if !ok {
		return 0, false
	}
	d, err := time.ParseDuration(strings.TrimSuffix(strings.Fields(rest + " ")[0], "."))
	if err != nil {
		return 0, true
	}
	return d, true
}

// PendingNotice is what the agent that asked hears when the remote's turn
// timed out: not an empty answer, but that the reply will follow.
func PendingNotice(handle string, waited time.Duration) string {
	within := ""
	if waited > 0 {
		within = " within " + waited.String()
	}
	return fmt.Sprintf("@%s has not replied%s; its reply will be forwarded automatically if it arrives (up to %d min).",
		handle, within, int(lateListen/time.Minute))
}

// LateForward is a timed-out task's reply, as handed to the agent that
// asked.
func LateForward(handle, taskID, text string) string {
	return fmt.Sprintf("Late reply from @%s for task %s (it timed out earlier):\n\n%s", handle, taskID, text)
}
