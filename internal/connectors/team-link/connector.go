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
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/login"
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
	// InRoom reports whether a session answers in a ticket room (or is a
	// sub-agent under one): such a session reaches its ticket only, so no
	// op here may set a teammate to work for it. Optional.
	InRoom func(sessionID string) bool
	// Lookup lists the sessions bound to a Slack thread (channel,
	// threadTS) as callerAgent asking from callerSession sees them.
	// Optional: session_lookup is unavailable without it.
	Lookup func(ctx context.Context, callerAgent, callerSession, channel, threadTS string) (any, error)
	// SetHold switches a session's take over (session Meta.TeamHold) on
	// or off; an error for a session that does not exist. Optional:
	// takeover is unavailable without it.
	SetHold func(ctx context.Context, sessionID string, on bool) error
	// ReadChat reads one page of a session's conversation in the tickets
	// ticket_conversation_read shape; read_chat checks access first.
	// Optional: read_chat is unavailable without it.
	ReadChat func(sessionID string, offset, limit int, order, detail string) (map[string]any, error)
}

// errInTicketRoom mirrors team.ErrInTicketRoom (this package does not
// import team).
var errInTicketRoom = errors.New("not available in a ticket room: this session reaches its ticket only — hand a room member a turn with @handle in the room")

// errTakeoverDenied refuses takeover of a session that is neither the
// caller's own nor its user's, for a caller who is no admin.
var errTakeoverDenied = errors.New("take over only this session, a session of your own user, or (admin) any session")

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
	Chat      string `wick:"desc=Run the message in this session of the teammate (session_id). On your own only with a session_id from a ticket (ticket_get / ticket_conversations, e.g. owner_session) or from session_lookup. Any other one (e.g. an older chat the chats op listed) ONLY when the user asks for it. Your own other chat (to = yourself) only when both chats are in the same ticket. A turn in a Slack thread session is posted in that thread. Omit to use the chat paired with this conversation."`
	NewChat   bool   `wick:"desc=true starts a NEW chat with the teammate (a clean context) instead of the one paired with this conversation, and pairs that new chat with it: later messages from this conversation go there. A Slack remote agent gets a new Slack thread — except one whose Remote target is a specific thread, which always posts in that thread."`
	TaskID    string `wick:"desc=Answer a task that came back state=input_required: pass its task_id (to and context_id may be omitted, and if given they must match that task)."`
	FanIn     string `wick:"dropdown=all|each|first;desc=How the replies of this message's group reach you. all (default): wait for every teammate of the group, then get ONE message with every reply — for one question to several teammates whose answers you combine. each: every reply on its own as it lands — for independent checks. first: the first answer only, the others are canceled — for the same question to several teammates when any one answer will do. Set by the group's first message, later ones keep it."`
	Group     string `wick:"desc=Keep a discussion across several teammates in one result: pass the group_id an earlier message of this conversation returned to join that group (also in a later turn, while it is still open). none = this task is independent and its reply comes on its own. Omit: the messages you send in one turn form one group."`
	Separate  bool   `wick:"desc=Only while you answer a task FROM this same teammate: true sends this as a separate message before you finish (refused otherwise, since your final answer already reaches them as the task's reply). A final answer that repeats it is not sent twice."`
}

type chatsInput struct {
	To string `wick:"desc=A teammate's handle: also list its recent chats with you (max 5, newest first) for an explicit resume. Omit to list only the chat paired with this conversation at each teammate."`
}

type getTaskInput struct {
	TaskID string `wick:"required;desc=A task_id returned by message."`
}

type listTasksInput struct{}

type takeoverInput struct {
	Chat string `wick:"desc=The session to take over or release (session_id). Omit for this session."`
	On   bool   `wick:"desc=true: take over (agent messages to the session are recorded, not run). false: back to auto."`
}

type sessionLookupInput struct {
	Permalink string `wick:"desc=A Slack thread link, e.g. https://<workspace>.slack.com/archives/C0123/p1792044355318329 (a ?thread_ts= in it wins). Or pass channel and thread_ts instead."`
	Channel   string `wick:"desc=The Slack channel id (C…), with thread_ts, when there is no permalink."`
	ThreadTS  string `wick:"desc=The thread's ts, e.g. 1792044355.318329, with channel."`
}

type readChatInput struct {
	To     string `wick:"required;desc=Handle of the agent whose session it is, e.g. @franky."`
	Chat   string `wick:"required;desc=The session to read (session_id), e.g. from a ticket (owner_session) or session_lookup."`
	Detail string `wick:"dropdown=final|trace;desc=final (default) = the messages only. trace = each assistant turn's tool calls and thinking as well — much larger, use it when the question is HOW something was done."`
	Order  string `wick:"dropdown=newest|oldest;desc=Which end to page from. newest (default) starts at the last message, oldest starts at the first. Either way a page reads oldest-first."`
	Limit  int    `wick:"desc=Messages per page. Default 20, maximum 50."`
	Offset int    `wick:"desc=Messages to skip from the chosen end — 0 is the first page. Use next_offset from the previous response rather than computing it."`
}

// readChatMaxLimit caps a read_chat page below ticket_conversation_read's
// 100: it reads into a turn that is about to answer, not a catch-up.
const readChatMaxLimit = 50

// Operations lists the connector's ops.
func Operations(deps Deps) []connector.Category {
	return []connector.Category{
		connector.Cat("Team", "Talk to the other agents of your Team.",
			connector.Op("message", "Message a Teammate",
				"Send a message to another agent of your Team. It returns within about 15 seconds: "+
					"{task_id, context_id, state, reply_text, reason, chat, group_id}; chat is the teammate's session this conversation is paired with (+ slack_thread for a Slack remote). "+
					"state=completed carries the reply (an empty reply_text means the teammate had nothing to add). "+
					"state=working means it is still on it: end your turn — the reply is delivered into this conversation when it lands; do not poll or resend. To ask several teammates, send all the messages first, then end your turn: the messages of one turn form a group (group_id), and its replies arrive together in ONE message (fan_in=all), so answer the user once with all of them. "+
					"fan_in=each gets every reply on its own (independent checks); fan_in=first takes the first answer and cancels the rest. group=<group_id> adds a later message to an open group; group=none keeps a message out of any group. "+
					"state=input_required means the teammate asks you something (reply_text): answer with message task_id=<that task_id>, or ask the user when only they can answer. "+
					"state=failed, rejected (refused: no access, not shared, mentions off) or canceled carries the reason. Pass context_id to continue the same exchange. "+
					"Each conversation of yours gets its own chat at the teammate, opened on your first message and reused after; set new_chat=true only when the user asks for a new/separate chat or session with the teammate. "+
					"chat=<session_id> runs the message in that session of the teammate: use it on your own only with a session_id that came from a ticket (ticket_get / ticket_conversations, e.g. owner_session) or from session_lookup; otherwise (e.g. continuing an older chat) only when the user asks. "+
					"You may message another chat of your own (to = yourself, chat=<its session_id>) only when both chats are in the same ticket. "+
					"A turn that runs in a Slack thread session is posted in that thread, so write what the thread may read (or [silent]). "+
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
			connector.Op("session_lookup", "Look Up a Thread's Sessions",
				"The wick sessions that answer in a Slack thread (permalink, or channel + thread_ts), newest first: "+
					"session_id, agent (its Team agent's handle, \"\" for none), project_id, ticket_id, owner_user, last_active, "+
					"can_send (whether you may message it: message to=@<agent> chat=<chat>), reason when you may not, and chat. "+
					"Only sessions of agents of your owner (or shared with them) are listed; hidden counts the others.",
				sessionLookupInput{}, deps.sessionLookup, wickdocs.Docs{}),
			connector.Op("read_chat", "Read a Teammate's Session",
				"Read, read-only, a session of a teammate you oversee: to=@<agent> chat=<session_id>. "+
					"Use it before you answer a checkpoint or send a correction, to see what the agent actually did and said in that session (e.g. its Slack thread session). "+
					"Allowed exactly when message to=@<agent> chat=<session_id> would be: the session exists and belongs to that agent, and is its user's (the owner's) session or in the same ticket as this conversation; for another chat of your own, only within one ticket. "+
					"Same output and paging as ticket_conversation_read: {session_id, title, detail, order, total, offset, limit, returned, messages, has_more, next_offset}. "+
					"limit default 20, max 50; order newest (default) or oldest; detail final (default) or trace. Nothing is sent and no chat is paired.",
				readChatInput{}, deps.readChat, wickdocs.Docs{}),
			connector.Op("takeover", "Take Over a Session",
				"Put a session under manual control (on=true) or back to auto (on=false). While on, a Team message another agent sends to that session runs no turn: "+
					"the sender's task ends rejected (\"session is under manual control (take over) — your message was recorded, not delivered\") and the handoff row records it. "+
					"A person's own @mention still runs. "+
					"Turn it on in your own session (chat omitted) when the user writes \"take over\" there, and off when they write \"lanjut auto\". "+
					"chat=<session_id> targets another session: only one of your own user's (or any, for an admin). Returns {chat, team_hold, by}; by is self, owner or admin.",
				takeoverInput{}, deps.takeover, wickdocs.Docs{}),
		),
	}
}

// caller resolves the hub and the calling agent from the SESSION.
func (d Deps) caller(c *connector.Ctx) (*teamlink.Hub, string, error) {
	if d.InRoom != nil && d.InRoom(c.SessionID()) {
		return nil, "", errInTicketRoom
	}
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
		FanIn:         strings.ToLower(strings.TrimSpace(c.Input("fan_in"))),
		Group:         strings.TrimSpace(c.Input("group")),
		Separate:      c.InputBool("separate"),
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

func (d Deps) sessionLookup(c *connector.Ctx) (any, error) {
	_, agentID, err := d.caller(c)
	if err != nil {
		return nil, err
	}
	if d.Lookup == nil {
		return nil, errUnavailable
	}
	channel, ts, err := ParseThread(c.Input("permalink"), c.Input("channel"), c.Input("thread_ts"))
	if err != nil {
		return nil, err
	}
	return d.Lookup(c.Context(), agentID, c.SessionID(), channel, ts)
}

func (d Deps) readChat(c *connector.Ctx) (any, error) {
	hub, agentID, err := d.caller(c)
	if err != nil {
		return nil, err
	}
	if d.ReadChat == nil {
		return nil, errUnavailable
	}
	user := ""
	if d.SessionUser != nil {
		user = d.SessionUser(c.SessionID())
	}
	chat := strings.TrimSpace(c.Input("chat"))
	if _, err := hub.ReadableChat(c.Context(), agentID, c.SessionID(), user, c.Input("to"), chat); err != nil {
		return nil, err
	}
	limit := c.InputInt("limit")
	if limit > readChatMaxLimit {
		limit = readChatMaxLimit
	}
	return d.ReadChat(chat, c.InputInt("offset"), limit, c.Input("order"), c.Input("detail"))
}

func (d Deps) takeover(c *connector.Ctx) (any, error) {
	if d.SetHold == nil {
		return nil, errUnavailable
	}
	caller := c.SessionID()
	chat := strings.TrimSpace(c.Input("chat"))
	if chat == "" {
		chat = caller
	}
	if chat == "" {
		return nil, errors.New("pass chat: no session to take over")
	}
	// A take over holds off other agents, so none of them may lift or set
	// it: an agent's session acts only on its own agent's sessions,
	// whoever owns them (an admin's agent included).
	if d.AgentOf != nil && caller != "" && chat != caller {
		if ca := d.AgentOf(c.Context(), caller); ca != "" && ca != d.AgentOf(c.Context(), chat) {
			return nil, errTakeoverDenied
		}
	}
	by := ""
	switch {
	case chat == caller:
		by = "self"
	case d.SessionUser != nil && caller != "" && d.SessionUser(chat) != "" && d.SessionUser(chat) == d.SessionUser(caller):
		by = "owner"
	default:
		if u := login.GetUser(c.Context()); u != nil && u.IsAdmin() {
			by = "admin"
		}
	}
	if by == "" {
		return nil, errTakeoverDenied
	}
	on := c.InputBool("on")
	if err := d.SetHold(c.Context(), chat, on); err != nil {
		return nil, err
	}
	return map[string]any{"chat": chat, "team_hold": on, "by": by}, nil
}

var (
	slackPermalinkPath = regexp.MustCompile(`^/archives/([A-Z0-9]+)/p(\d{10})(\d{6})/?$`)
	slackTS            = regexp.MustCompile(`^\d{10}\.\d{6}$`)
)

// ParseThread resolves session_lookup's input to a Slack channel and
// thread ts: a permalink (https://<ws>.slack.com/archives/<C>/p<16
// digits>, its p split 10.6; a thread_ts query wins, it names the thread
// a reply link sits in), else channel + threadTS.
func ParseThread(permalink, channel, threadTS string) (string, string, error) {
	permalink, channel, threadTS = strings.TrimSpace(permalink), strings.TrimSpace(channel), strings.TrimSpace(threadTS)
	if permalink != "" {
		u, err := url.Parse(permalink)
		if err != nil || !strings.HasSuffix(strings.ToLower(u.Hostname()), "slack.com") {
			return "", "", fmt.Errorf("permalink %q is not a Slack link", permalink)
		}
		m := slackPermalinkPath.FindStringSubmatch(u.Path)
		if m == nil {
			return "", "", fmt.Errorf("permalink %q is not a Slack message link (…/archives/<channel>/p<ts>)", permalink)
		}
		channel, threadTS = m[1], m[2]+"."+m[3]
		if q := strings.TrimSpace(u.Query().Get("thread_ts")); q != "" {
			threadTS = q
		}
	}
	if channel == "" || threadTS == "" {
		return "", "", errors.New("pass permalink, or channel and thread_ts")
	}
	if !slackTS.MatchString(threadTS) {
		return "", "", fmt.Errorf("thread_ts %q is not a Slack ts (e.g. 1792044355.318329)", threadTS)
	}
	return channel, threadTS, nil
}
