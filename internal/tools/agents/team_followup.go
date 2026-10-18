package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
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
		text, at, ok := lastOwnTurn(globalLayout.SessionConversation(sessionID))
		if ok {
			hub.FollowUpAfter(context.Background(), sessionID, text, at)
		}
	})
}

// lastOwnTurn reads the newest assistant turn of a conversation.jsonl and
// the message that started it. ok is true when that reply has text, is
// not [silent], was not cut short, and its trigger is one of
// followUpSources.
func lastOwnTurn(path string) (text string, at time.Time, ok bool) {
	turns := tailTurns(path, 64<<10)
	reply := -1
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "assistant" {
			reply = i
			break
		}
	}
	if reply < 0 {
		return "", time.Time{}, false
	}
	a := turns[reply]
	body, silent := teamlink.StripSilent(a.Text)
	if silent || a.Interrupted || body == "" {
		return "", time.Time{}, false
	}
	for i := reply - 1; i >= 0; i-- {
		switch turns[i].Role {
		case "assistant":
			// Two replies in a row: the second has no trigger of its own.
			return "", time.Time{}, false
		case "user":
			if !followUpSources[turns[i].Source] {
				return "", time.Time{}, false
			}
			return body, a.Timestamp, true
		}
	}
	return "", time.Time{}, false
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
