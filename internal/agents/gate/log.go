package gate

import (
	"time"

	"github.com/yogasw/wick/internal/agents/storage"
)

// Entry is one row appended to the shared commands.jsonl. Each gate
// invocation may emit multiple entries — one per stage it goes
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
//
// SessionID is best-effort metadata derived by the daemon from the
// hook payload's cwd at routing time; the gate binary itself doesn't
// know which wick session triggered the call so it leaves the field
// empty and lets the daemon populate it via the post-decision write.
type Entry struct {
	Timestamp time.Time `json:"ts"`
	Stage     string    `json:"stage,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	Cmd       string    `json:"cmd"`
	Status    string    `json:"status"`
	Decision  string    `json:"decision,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	MatchKey  string    `json:"match_key,omitempty"`
	WorkDir   string    `json:"work_dir,omitempty"`
}

// Append writes one entry to the shared commands.jsonl for appName.
// Used by both the gate binary (post-decision) and any in-proc gate
// logic that wants to record without going through the binary.
//
// Pre-Stage 9 the log lived per-session under
// `sessions/<id>/commands.jsonl`; now it's a single app-wide file.
// The Entry.SessionID field carries the disambiguator for UI grouping.
func Append(appName string, entry Entry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	return storage.AppendJSONL(
		SharedCommandsPath(appName),
		"wick-cmd-v1",
		"",
		entry,
	)
}
