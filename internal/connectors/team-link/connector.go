// Package teamlink exposes Team-agent messaging as a fixed,
// single-instance connector, the way sub-agents exposes delegation.
//
// Its ops also surface as top-level team_<op> tools (see
// internal/mcp/handlers/wickmanager.go), offered only in a Team agent's
// session that has at least one teammate it can reach. The transport
// behind them is A2A — see internal/agents/teamlink.
package teamlink

import (
	"context"
	"errors"
	"strings"

	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// Key is the connector definition slug.
const Key = "team"

// Configs is empty: the connector drives in-process wick services.
type Configs struct{}

// Deps wires the connector to the running Hub.
type Deps struct {
	// Hub is late-bound: the connector is registered before the Hub exists.
	Hub func() *teamlink.Hub
	// AgentOf returns the Team agent a session belongs to, "" for none.
	AgentOf func(ctx context.Context, sessionID string) string
	// SessionUser returns the wick user of a session, "" when unknown:
	// whose chats the chats op lists. Optional.
	SessionUser func(sessionID string) string
}

var errUnavailable = errors.New("team messaging is not configured on this server")

// Meta returns the static metadata block for the registry.
func Meta() connector.Meta {
	return connector.Meta{
		Key:  Key,
		Name: "Team",
		Description: "Message another agent of your Team and get its reply. Team agents are persistent " +
			"colleagues with their own chat and access, not sub-agents.",
		Icon:  "🤝",
		Fixed: true,
	}
}

// Module returns the fully-wired connector.Module. Platform-tagged, so a
// Team agent has it on by default and its owner can switch it off in
// the agent's Access tab.
func Module(deps Deps) connector.Module {
	m := Meta()
	m.DefaultTags = []tool.DefaultTag{tags.Connector, tags.Platform}
	return connector.Module{Meta: m, Operations: Operations(deps)}
}

type messageInput struct {
	To        string `wick:"required;desc=The teammate's handle, e.g. @anton. Agents of your own Team, or agents shared with your owner."`
	Message   string `wick:"required;textarea;desc=What YOU need from them, composed by you. Lead with the point. wick signs it with your name — do not prefix it."`
	ContextID string `wick:"desc=Continue an earlier exchange: pass the context_id a previous call returned."`
	Chat      string `wick:"desc=Resume one of the teammate's older chats: a session_id the chats op listed. ONLY when the user asks to continue that chat, never pick one on your own. Omit to use the chat paired with this conversation."`
	NewChat   bool   `wick:"desc=true starts a NEW chat with the teammate (a clean context) instead of the one paired with this conversation, and pairs that new chat with it: later messages from this conversation go there. A Slack remote agent gets a new Slack thread — except one whose Remote target is a specific thread, which always posts in that thread."`
	TaskID    string `wick:"desc=Answer a task that came back state=input_required: pass its task_id (to and context_id may be omitted, and if given they must match that task)."`
}

type chatsInput struct {
	To string `wick:"desc=A teammate's handle: also list its recent chats with you (max 5, newest first) for an explicit resume. Omit to list only the chat paired with this conversation at each teammate."`
}

type getTaskInput struct {
	TaskID string `wick:"required;desc=A task_id returned by message."`
}

type listTasksInput struct{}

// Operations lists the connector's ops.
func Operations(deps Deps) []connector.Category {
	return []connector.Category{
		connector.Cat("Team", "Talk to the other agents of your Team.",
			connector.Op("message", "Message a Teammate",
				"Send a message to another agent of your Team. It returns within about 15 seconds: "+
					"{task_id, context_id, state, reply_text, reason, chat}; chat is the teammate's session this conversation is paired with (+ slack_thread for a Slack remote). "+
					"state=completed carries the reply (an empty reply_text means the teammate had nothing to add). "+
					"state=working means it is still on it: end your turn — the reply is delivered into this conversation when it lands; do not poll or resend. To ask several teammates, send all the messages first, then end your turn. "+
					"state=input_required means the teammate asks you something (reply_text): answer with message task_id=<that task_id>, or ask the user when only they can answer. "+
					"state=failed, rejected (refused: no access, not shared, mentions off) or canceled carries the reason. Pass context_id to continue the same exchange. "+
					"Each conversation of yours gets its own chat at the teammate, opened on your first message and reused after; set new_chat=true only when the user asks for a new/separate chat or session with the teammate, and chat=<session_id> only when the user asks to continue an older one — never on your own. "+
					"Agent-to-agent turns are capped per exchange; on \"hop limit reached\" stop and report to the user.",
				messageInput{}, deps.message, wickdocs.Docs{}),
			connector.Op("chats", "List Team Chats",
				"The chat paired with this conversation at each teammate (agent_id, handle, chat.session_id, chat.slack_thread for a Slack remote). "+
					"With to: also that teammate's recent chats with you (title, last_active; max 5), so the user can pick one to continue via message chat=<session_id>.",
				chatsInput{}, deps.chats, wickdocs.Docs{}),
			connector.Op("get_task", "Check a Team Task",
				"Read the state (and reply, question or reason, once there) of a task you sent with message, kept 7 days. "+
					"Use it to check one task — when the user asks, or when a tool call was cut off or you are unsure the reply arrived — not in a polling loop: replies are delivered to you on their own.",
				getTaskInput{}, deps.getTask, wickdocs.Docs{}),
			connector.Op("list_tasks", "List Team Tasks",
				"The tasks this conversation sent to teammates (kept 7 days), newest first: task_id, to_handle, state, age, title and a one-line summary of the reply, question or reason. "+
					"Check it before re-sending a message whose answer you did not see.",
				listTasksInput{}, deps.listTasks, wickdocs.Docs{}),
			connector.Op("cancel_task", "Cancel a Team Task",
				"Cancel a task you sent that is still working or waiting for your answer: the teammate's turn for it is stopped (or the task leaves the line if it has not started; any other turn of the teammate keeps running) and the task ends canceled. "+
					"Use it when the user calls the request off or it is no longer needed.",
				getTaskInput{}, deps.cancelTask, wickdocs.Docs{}),
		),
	}
}

// caller resolves the hub and the calling agent from the SESSION.
func (d Deps) caller(c *connector.Ctx) (*teamlink.Hub, string, error) {
	if d.Hub == nil || d.Hub() == nil || d.AgentOf == nil {
		return nil, "", errUnavailable
	}
	agentID := d.AgentOf(c.Context(), c.SessionID())
	if agentID == "" {
		return nil, "", teamlink.ErrNotTeamSession
	}
	return d.Hub(), agentID, nil
}

func (d Deps) message(c *connector.Ctx) (any, error) {
	hub, agentID, err := d.caller(c)
	if err != nil {
		return nil, err
	}
	// wait_seconds is gone: a message waits QuickWait at most. An older
	// prompt that still sends it is not refused.
	user := ""
	if d.SessionUser != nil {
		user = d.SessionUser(c.SessionID())
	}
	return hub.Send(c.Context(), teamlink.SendInput{
		SessionUser:   user,
		CallerSession: c.SessionID(),
		CallerAgentID: agentID,
		To:            c.Input("to"),
		Text:          c.Input("message"),
		ContextID:     strings.TrimSpace(c.Input("context_id")),
		TaskID:        strings.TrimSpace(c.Input("task_id")),
		NewChat:       c.InputBool("new_chat"),
		Chat:          strings.TrimSpace(c.Input("chat")),
	})
}

func (d Deps) chats(c *connector.Ctx) (any, error) {
	hub, agentID, err := d.caller(c)
	if err != nil {
		return nil, err
	}
	user := ""
	if d.SessionUser != nil {
		user = d.SessionUser(c.SessionID())
	}
	if to := strings.TrimSpace(c.Input("to")); to != "" {
		return hub.RecentChats(c.Context(), agentID, c.SessionID(), user, to)
	}
	linked, err := hub.LinkedChats(c.Context(), agentID, c.SessionID(), user)
	if err != nil {
		return nil, err
	}
	return map[string]any{"linked_chats": linked}, nil
}

func (d Deps) getTask(c *connector.Ctx) (any, error) {
	hub, agentID, err := d.caller(c)
	if err != nil {
		return nil, err
	}
	return hub.GetTask(c.Context(), agentID, strings.TrimSpace(c.Input("task_id")))
}

func (d Deps) listTasks(c *connector.Ctx) (any, error) {
	hub, _, err := d.caller(c)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tasks": hub.SentFrom(c.SessionID())}, nil
}

func (d Deps) cancelTask(c *connector.Ctx) (any, error) {
	hub, agentID, err := d.caller(c)
	if err != nil {
		return nil, err
	}
	return hub.CancelTask(c.Context(), agentID, strings.TrimSpace(c.Input("task_id")))
}
