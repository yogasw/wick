package teamlink

import (
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// Every task keeps a short log of what was said on it — the brief, the
// teammate's replies and questions, each answer — so the UI can show the
// task's thread. The Hub writes it as the messages pass through, never by
// reading the teammate's chat: a remote, Slack or plugin teammate has no
// chat wick can read.
const (
	// maxThreadEntries caps a task's log; the oldest entries go first.
	maxThreadEntries = 50
	// threadTextMax caps one entry's text, like the reply preview.
	threadTextMax = replyPreviewMax
)

// Roles of a ThreadEntry.
const (
	// ThreadCaptain is the agent that sent the task.
	ThreadCaptain = "captain"
	// ThreadUser is the person in the sending chat.
	ThreadUser = "user"
	// ThreadTeammate is the teammate the task went to.
	ThreadTeammate = "teammate"
)

// ThreadEntry is one message of a task's thread.
type ThreadEntry struct {
	Role string `json:"role"`
	// Handle and Name name the agent that wrote it; empty for the user.
	Handle string    `json:"handle,omitempty"`
	Name   string    `json:"name,omitempty"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

// logLocked adds a message to ref's thread in time order, keeping the
// newest maxThreadEntries: a teammate's reply can settle before Send
// registered the message it answers, which then still goes ahead of it
// (and ahead of a reply dated the same instant).
func (r *taskRef) logLocked(e ThreadEntry) {
	if e.Text = capText(e.Text, threadTextMax); e.Text == "" {
		return
	}
	i := len(r.log)
	for i > 0 && (r.log[i-1].At.After(e.At) ||
		(r.log[i-1].At.Equal(e.At) && e.Role != ThreadTeammate && r.log[i-1].Role == ThreadTeammate)) {
		i--
	}
	r.log = append(r.log, ThreadEntry{})
	copy(r.log[i+1:], r.log[i:])
	r.log[i] = e
	if n := len(r.log) - maxThreadEntries; n > 0 {
		r.log = append([]ThreadEntry(nil), r.log[n:]...)
	}
}

// logTeammateLocked adds what ref's teammate said.
func (r *taskRef) logTeammateLocked(text string, at time.Time) {
	r.logLocked(ThreadEntry{Role: ThreadTeammate, Handle: r.to.Handle, Name: r.to.Name, Text: text, At: at})
}

// TaskThread is the thread of a task sessionID sent, oldest first;
// ErrUnknownTask for any other task, as userTaskLocked.
func (h *Hub) TaskThread(sessionID, taskID string) ([]ThreadEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ref, err := h.userTaskLocked(sessionID, a2a.TaskID(strings.TrimSpace(taskID)))
	if err != nil {
		return nil, err
	}
	return append([]ThreadEntry{}, ref.log...), nil
}
