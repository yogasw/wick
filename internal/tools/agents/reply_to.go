package agents

import (
	"errors"
	"fmt"
	"strings"

	agentstore "github.com/yogasw/wick/internal/agents/store"
)

// replyToIDMax bounds the reply_to id a client may send. Real ids are a
// UnixNano or "turn-<n>"; anything longer is not one of ours.
const replyToIDMax = 64

// errReplyTo is every reason a reply_to is refused. One message for all of
// them: the client learns the id is unusable, not why.
var errReplyTo = errors.New("reply_to: message not found in this chat")

// resolveReplyTo turns the reply_to id a person sent with a message into
// the quote stored on their turn. The id must name a turn of THIS session
// (the caller has already passed ownsSession for it), looked up the same
// way the conversation API numbers turns, so a reply can only point at
// something the caller can read. Author and Excerpt come from the stored
// turn; nothing the client sent besides the id is used.
func resolveReplyTo(sessionID, id string, speaker *agentstore.Speaker) (*agentstore.ReplyTo, error) {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > replyToIDMax {
		return nil, errReplyTo
	}
	turns, err := loadConversation(globalLayout, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load conversation: %w", err)
	}
	backfillTurnIDs(turns)
	stampSpeakers(turns, speaker)
	for i := range turns {
		if turns[i].TurnID != id {
			continue
		}
		return replyFromTurn(turns[i])
	}
	return nil, errReplyTo
}

// replyFromTurn builds the quote of one stored turn. A turn with nothing
// to quote (an empty system notice) is refused like a missing one.
func replyFromTurn(t agentstore.ConversationTurn) (*agentstore.ReplyTo, error) {
	text := t.Text
	if t.Postback != nil {
		text = t.Postback.Label
		if text == "" {
			text = t.Postback.Value
		}
	}
	excerpt := agentstore.ReplyExcerpt(text)
	if excerpt == "" && len(t.Attachments) > 0 {
		excerpt = agentstore.ReplyExcerpt(t.Attachments[0].Name)
	}
	if excerpt == "" {
		return nil, errReplyTo
	}
	return &agentstore.ReplyTo{
		TurnID:  t.TurnID,
		Role:    t.Role,
		Author:  replyAuthor(t),
		Excerpt: excerpt,
	}, nil
}

// replyAuthor names who said the quoted turn, from server-side fields only.
func replyAuthor(t agentstore.ConversationTurn) string {
	switch t.Role {
	case "assistant":
		if t.Speaker != nil && t.Speaker.Handle != "" {
			return "@" + t.Speaker.Handle
		}
		if t.Agent != "" {
			return t.Agent
		}
		return "assistant"
	case "user":
		if s := t.Sender; s != nil {
			switch {
			case s.Name != "":
				return s.Name
			case s.Handle != "":
				return "@" + s.Handle
			}
		}
		if t.Source == sourceTeam {
			return "teammate"
		}
		return "user"
	}
	return "wick"
}
