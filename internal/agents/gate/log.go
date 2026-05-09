package gate

import (
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// Entry is one row appended to sessions/<id>/commands.jsonl. Each
// gate invocation may emit multiple entries — one per stage it goes
// through (received → dispatched → resolved → decided). The stages
// give the operator a full audit trail when something looks wrong:
// "I clicked Approve but the command was blocked anyway" → walk the
// stages and find the gap.
//
// Stages (Status field, when not "allowed" / "blocked"):
//   - "received"      gate process started, spec loaded, cmd parsed
//   - "stdin_error"   stdin parse / timeout / spec missing — terminal
//   - "auto_allowed"  whitelist or AutoApproved hit; no socket call
//   - "socket_dial"   about to dial daemon socket
//   - "socket_sent"   ApprovalRequest written to socket
//   - "socket_recv"   ApprovalResponse read from socket
//   - "socket_error"  any socket-level failure — terminal "blocked"
//   - "allowed"       final decision: command ran (or will run)
//   - "blocked"       final decision: claude saw exit 2
//
// The Status="allowed" / "blocked" line is the one the UI displays
// in the Commands tab; intermediate stages are kept for debugging.
type Entry struct {
	Timestamp time.Time `json:"ts"`
	Stage     string    `json:"stage,omitempty"`      // see comment above; empty for legacy "allowed/blocked"
	Agent     string    `json:"agent,omitempty"`
	Tool      string    `json:"tool,omitempty"`       // "Bash" / "Edit" / ...
	Cmd       string    `json:"cmd"`
	Status    string    `json:"status"`               // "allowed" | "blocked" (terminal only)
	Decision  string    `json:"decision,omitempty"`   // "approve_once" / "approve_session" / ...
	Reason    string    `json:"reason,omitempty"`
	RequestID string    `json:"request_id,omitempty"` // socket request UUID, ties stages together
	MatchKey  string    `json:"match_key,omitempty"`  // sha256 used by always-allow lookup
}

// Append writes one entry to sessions/<sessionID>/commands.jsonl.
// Used by both the gate binary (post-decision) and any in-proc
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
