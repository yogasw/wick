package gate

import (
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// Entry is one row appended to sessions/<id>/commands.jsonl. Format
// matches §4.5 of the design doc:
//
//	{"ts":"...","agent":"backend","cmd":"rm -rf .","status":"blocked","reason":"..."}
type Entry struct {
	Timestamp time.Time `json:"ts"`
	Agent     string    `json:"agent,omitempty"`
	Cmd       string    `json:"cmd"`
	Status    string    `json:"status"` // "allowed" | "blocked"
	Reason    string    `json:"reason,omitempty"`
}

// Append writes one entry to sessions/<sessionID>/commands.jsonl.
// Used by both the wick-gate binary (post-decision) and any in-proc
// gate logic that wants to record without going through the binary.
func Append(layout config.Layout, sessionID string, entry Entry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	return storage.AppendJSONL(
		layout.SessionCommands(sessionID),
		"wick-cmd-v1",
		sessionID,
		entry,
	)
}
