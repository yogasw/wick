package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/teamlink"
)

// A local agent that answered a task and kept working — its sub-agents,
// a schedule, a job reporting back — says the result in a later turn of
// the same chat. That turn is no task's, so nothing carried it back: the
// asker had the first answer (often [silent]) and never the report. Each
// such turn's reply now reaches the asker as a follow-up of that task
// (teamlink.Hub.FollowUpAfter). A [silent] turn stays put: a checkpoint
// with nothing new is no report.

// followUpSettle is how long after a turn's Done its reply is read from
// conversation.jsonl: the turn is saved by then.
const followUpSettle = 1500 * time.Millisecond

// followUpSources are the triggers of a turn the agent runs on its own
// behalf. A person's message (web chat, Slack, Telegram, an action card)
// starts that person's turn, and a task's own turn answers the task.
var followUpSources = map[string]bool{
	sourceTeam: true, // a teammate's reply to a task this chat sent
	"subagent": true, // a sub-agent's result
	"schedule": true, // a scheduled message or watch
	"agent":    true, // the agent's own follow-up
	"cli":      true, // a job reporting back (support-tools agent send)
}

// teamTaskTurns marks the sessions whose running turn belongs to a team
// task: that turn's reply is the task's, never a follow-up.
var teamTaskTurns sync.Map

// noteTeamTaskTurn marks sessionID's running turn as a task's when the
// task gate says so; called on the turn's activity.
func noteTeamTaskTurn(sessionID string) {
	if teamTurnGate.Owner(sessionID) != "" {
		teamTaskTurns.Store(sessionID, true)
	}
}

// teamFollowUpOnTurnEnd hands the reply of the turn that just ended in
// sessionID to the asker of the task that session answered, when the
// turn was the agent's own (see followUpSources) and not [silent].
func teamFollowUpOnTurnEnd(hub *teamlink.Hub, sessionID string) {
	_, owned := teamTaskTurns.LoadAndDelete(sessionID)
	if hub == nil || owned || teamTurnGate.Owner(sessionID) != "" || !hub.HasAnswered(sessionID) {
		return
	}
	time.AfterFunc(followUpSettle, func() {
		t, ok := lastOwnTurn(globalLayout.SessionConversation(sessionID))
		if ok {
			hub.FollowUpAfter(context.Background(), sessionID, t.text, t.at, t.personAt)
		}
	})
}

// ownTurn is the newest reply of a chat that may be a follow-up: its
// text, when it was said, and when a person last wrote in the chat (zero
// when none did in the window read).
type ownTurn struct {
	text     string
	at       time.Time
	personAt time.Time
}

// teamDelivery reports whether a sub-agent-path turn is a teammate's
// answer to a task THIS chat sent ("Reply from", "Follow-up from", the
// "[team results …]" fan-in). The turn it starts is this chat acting as
// an asker of someone else: its reply is not news for the task the chat
// answered. Before this, the Yelan chat Yoga works in had answered one of
// Captain's tasks, and every summary it wrote Yoga after a Captain reply
// went back to Captain as "Follow-up from Yelan".
func teamDelivery(t store.ConversationTurn) bool {
	if t.Source != "subagent" {
		return false
	}
	return strings.HasPrefix(t.Text, "Reply from ") || strings.HasPrefix(t.Text, "Follow-up from ") ||
		strings.HasPrefix(t.Text, "[team results")
}

// lastOwnTurn reads the newest assistant turn of a conversation.jsonl and
// the message that started it. ok is true when that reply has text, is
// not [silent], was not cut short, and its trigger is one of
// followUpSources and not a teammate's answer to this chat's own task
// (teamDelivery). personAt is the newest turn a person wrote, so the hub
// can tell a chat a person has taken over since the task ended.
func lastOwnTurn(path string) (ownTurn, bool) {
	turns := tailTurns(path, 64<<10)
	var personAt time.Time
	for _, t := range turns {
		if t.Role == "user" && !followUpSources[t.Source] && t.Timestamp.After(personAt) {
			personAt = t.Timestamp
		}
	}
	reply := -1
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "assistant" {
			reply = i
			break
		}
	}
	if reply < 0 {
		return ownTurn{}, false
	}
	a := turns[reply]
	body, silent := teamlink.StripSilent(a.Text)
	if silent || a.Interrupted || body == "" {
		return ownTurn{}, false
	}
	for i := reply - 1; i >= 0; i-- {
		switch turns[i].Role {
		case "assistant":
			// Two replies in a row: the second has no trigger of its own.
			return ownTurn{}, false
		case "user":
			if !followUpSources[turns[i].Source] || teamDelivery(turns[i]) {
				return ownTurn{}, false
			}
			return ownTurn{text: body, at: a.Timestamp, personAt: personAt}, true
		}
	}
	return ownTurn{}, false
}

// tailTurns parses the turns in the last n bytes of a conversation.jsonl,
// oldest first. A line cut by the window, or any other bad line, is
// skipped.
func tailTurns(path string, n int64) []store.ConversationTurn {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	off := max(st.Size()-n, 0)
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil
	}
	var out []store.ConversationTurn
	for _, line := range bytes.Split(buf, []byte{'\n'}) {
		var t store.ConversationTurn
		if len(line) == 0 || json.Unmarshal(line, &t) != nil || t.Role == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}
