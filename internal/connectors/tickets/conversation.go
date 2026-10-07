package tickets

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	agentstore "github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/pkg/connector"
)

/* Reading the conversations a ticket already holds.

   A ticket is the unit of work and a session is one conversation about it,
   so the thing a fresh session most often needs is not the ticket's fields
   but what was already SAID on it — the previous attempt, what it tried,
   where it stopped. Until now that was unreachable: the ticket answered with
   a list of session ids and nothing that could be done with them.

   Two ops rather than one, and deliberately so. A long-running ticket holds
   several conversations of a few hundred turns each; an op that answered
   "the conversation" would have to pick one and would return more than any
   caller asked for. So: LIST what is there (cheap, one line each), then READ
   the one you chose, a page at a time.

   Two defaults carry the weight:
   - detail=final — the assistant's replies and the user's messages, not the
     tool calls. The trace is available (detail=trace) but it is an order of
     magnitude bigger, and "what did we conclude" is the usual question.
   - order=newest — the tail of the conversation, because catching up starts
     at the end. Pages walk BACKWARDS from there while each page still reads
     forwards, which is how a person re-reads a thread. */

// convTurnTextCap bounds one message's text. A single turn can hold a whole
// generated document; a page of twenty would then be unreadable and blow the
// tool result. The cut is marked, and the full turn is one session-file read
// away for anyone who needs it.
const convTurnTextCap = 4000

// Trace events are summaries, not payloads: enough to see WHICH tool ran and
// roughly on what. A caller that needs a tool's full output is looking at the
// wrong op.
const (
	convEventTextCap  = 500
	convMaxEventsTurn = 40
)

const convDefaultLimit = 20

type conversationsInput struct {
	TicketID  string `wick:"desc=Ticket whose conversations to list. Defaults to the ticket the calling session belongs to."`
	ProjectID string `wick:"desc=Project the ticket belongs to. Defaults to the calling session's project."`
}

type conversationReadInput struct {
	SessionID string `wick:"desc=Conversation to read, from ticket_conversations. Optional when the ticket holds exactly one."`
	TicketID  string `wick:"desc=Ticket the conversation belongs to. Defaults to the ticket the calling session belongs to."`
	ProjectID string `wick:"desc=Project the ticket belongs to. Defaults to the calling session's project."`
	Detail    string `wick:"dropdown=final|trace;desc=final (default) = the messages only. trace = each assistant turn's tool calls and thinking as well — much larger, use it when the question is HOW something was done."`
	Order     string `wick:"dropdown=newest|oldest;desc=Which end to page from. newest (default) starts at the last message, oldest starts at the first. Either way a page reads oldest-first."`
	Limit     int    `wick:"desc=Messages per page. Default 20, maximum 100."`
	Offset    int    `wick:"desc=Messages to skip from the chosen end — 0 is the first page. Use next_offset from the previous response rather than computing it."`
}

// conversationView is one row of the list: enough to choose between them
// without reading any of them.
type conversationView struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title,omitempty"`
	Status    string `json:"status,omitempty"`
	Origin    string `json:"origin,omitempty"`
	OwnerID   string `json:"owner_user_id,omitempty"`
	OwnerName string `json:"owner_name,omitempty"`
	Messages  int    `json:"messages"`
	StartedAt string `json:"started_at,omitempty"`
	LastAt    string `json:"last_message_at,omitempty"`
	// Current marks the caller's own conversation, so an agent reading its
	// ticket's history does not report its own turns back as prior work.
	Current bool `json:"is_current,omitempty"`
}

type conversationEvent struct {
	Type    string `json:"type"`
	Tool    string `json:"tool,omitempty"`
	Input   string `json:"input,omitempty"`
	Text    string `json:"text,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
	At      string `json:"at,omitempty"`
}

type conversationMessage struct {
	Index int    `json:"index"`
	Role  string `json:"role"`
	At    string `json:"at,omitempty"`
	// From names the human behind a user turn as the channel resolved them —
	// never parsed out of the text. Empty on assistant and system turns.
	From        string              `json:"from,omitempty"`
	Agent       string              `json:"agent,omitempty"`
	Provider    string              `json:"provider,omitempty"`
	Kind        string              `json:"kind,omitempty"`
	Text        string              `json:"text,omitempty"`
	Truncated   bool                `json:"truncated,omitempty"`
	Interrupted bool                `json:"interrupted,omitempty"`
	IsError     bool                `json:"is_error,omitempty"`
	Events      []conversationEvent `json:"events,omitempty"`
	EventsMore  int                 `json:"events_omitted,omitempty"`
}

func (h *handlers) conversations(c *connector.Ctx) (any, error) {
	projectID, err := resolveProject(h.layout, c, c.Input("project_id"))
	if err != nil {
		return nil, err
	}
	tk, err := resolveTicket(h.layout, c, projectID, c.Input("ticket_id"))
	if err != nil {
		return nil, err
	}

	caller := c.SessionID()
	out := make([]conversationView, 0, len(tk.Sessions))
	for _, sid := range tk.Sessions {
		v := conversationView{SessionID: sid, Current: sid == caller}
		if sess, err := session.Load(h.layout, sid); err == nil {
			v.Title = sess.Meta.Label
			v.Status = string(sess.Meta.Status)
			v.Origin = string(sess.Meta.Origin)
			v.OwnerID = sess.Meta.UserID
			v.OwnerName = c.UserName(sess.Meta.UserID)
		}
		turns, err := h.readTurns(sid)
		if err == nil {
			v.Messages = len(turns)
			if len(turns) > 0 {
				v.StartedAt = fmtTime(turns[0].Timestamp)
				v.LastAt = fmtTime(turns[len(turns)-1].Timestamp)
			}
		}
		out = append(out, v)
	}
	// Most recently active first: the conversation worth opening is almost
	// always the one that was last spoken in, and the ticket stores them in
	// attach order.
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastAt > out[j].LastAt })

	return map[string]any{
		"ticket_id":     tk.ID,
		"project_id":    projectID,
		"ticket_title":  tk.Title,
		"conversations": out,
		"total":         len(out),
		"read_with":     "ticket_conversation_read(session_id=…)",
	}, nil
}

func (h *handlers) conversationRead(c *connector.Ctx) (any, error) {
	projectID, err := resolveProject(h.layout, c, c.Input("project_id"))
	if err != nil {
		return nil, err
	}
	tk, err := resolveTicket(h.layout, c, projectID, c.Input("ticket_id"))
	if err != nil {
		return nil, err
	}
	sid, err := pickConversation(tk, c.Input("session_id"))
	if err != nil {
		return nil, err
	}

	turns, err := h.readTurns(sid)
	if err != nil {
		return nil, fmt.Errorf("read conversation %s: %w", sid, err)
	}

	limit := c.InputInt("limit")
	if limit <= 0 {
		limit = convDefaultLimit
	}
	if limit > 100 {
		limit = 100
	}
	offset := c.InputInt("offset")
	if offset < 0 {
		offset = 0
	}
	order := strings.TrimSpace(strings.ToLower(c.Input("order")))
	if order != "oldest" {
		order = "newest"
	}
	trace := strings.EqualFold(strings.TrimSpace(c.Input("detail")), "trace")

	total := len(turns)
	start, end := pageBounds(total, offset, limit, order)

	msgs := make([]conversationMessage, 0, end-start)
	for i := start; i < end; i++ {
		msgs = append(msgs, h.message(sid, i, turns[i], trace))
	}

	res := map[string]any{
		"ticket_id":  tk.ID,
		"project_id": projectID,
		"session_id": sid,
		"detail":     map[bool]string{true: "trace", false: "final"}[trace],
		"order":      order,
		"total":      total,
		"offset":     offset,
		"limit":      limit,
		"returned":   len(msgs),
		"messages":   msgs,
	}
	if sess, err := session.Load(h.layout, sid); err == nil && sess.Meta.Label != "" {
		res["title"] = sess.Meta.Label
	}
	// Say where the next page is rather than leaving the caller to work out
	// which end it was paging from.
	if more := (order == "newest" && start > 0) || (order == "oldest" && end < total); more {
		res["has_more"] = true
		res["next_offset"] = offset + len(msgs)
	}
	return res, nil
}

// pickConversation resolves which of the ticket's sessions to read. Only the
// ticket's own sessions are reachable: this op exists to read the work on a
// ticket, not to be a way to read any conversation on the install by id.
func pickConversation(tk ticket.Ticket, explicit string) (string, error) {
	want := strings.TrimSpace(explicit)
	if want == "" {
		switch len(tk.Sessions) {
		case 0:
			return "", fmt.Errorf("ticket %s has no conversation attached yet", tk.ID)
		case 1:
			return tk.Sessions[0], nil
		default:
			return "", fmt.Errorf("ticket %s holds %d conversations — pass session_id (ticket_conversations lists them)",
				tk.ID, len(tk.Sessions))
		}
	}
	for _, sid := range tk.Sessions {
		if sid == want {
			return sid, nil
		}
	}
	return "", fmt.Errorf("session %q is not attached to ticket %s — ticket_conversations lists the ones that are", want, tk.ID)
}

// pageBounds turns (offset, limit) into a half-open turn range. Both orders
// return an ASCENDING range: only which end the offset counts from differs,
// so a page always reads forwards even while paging backwards.
func pageBounds(total, offset, limit int, order string) (int, int) {
	if total == 0 || offset >= total {
		return 0, 0
	}
	if order == "oldest" {
		start := offset
		end := start + limit
		if end > total {
			end = total
		}
		return start, end
	}
	end := total - offset
	start := end - limit
	if start < 0 {
		start = 0
	}
	return start, end
}

func (h *handlers) message(sessionID string, idx int, t agentstore.ConversationTurn, trace bool) conversationMessage {
	m := conversationMessage{
		Index:       idx,
		Role:        t.Role,
		At:          fmtTime(t.Timestamp),
		Agent:       t.Agent,
		Provider:    t.Provider,
		Kind:        t.Kind,
		Interrupted: t.Interrupted,
		IsError:     t.IsError,
	}
	if t.Sender != nil {
		m.From = senderLabel(t.Sender)
	}
	m.Text, m.Truncated = clip(t.Text, convTurnTextCap)
	if t.Truncated {
		m.Truncated = true
	}
	if trace && t.Role == "assistant" {
		m.Events, m.EventsMore = h.traceEvents(sessionID, t)
	}
	return m
}

// traceEvents summarises one assistant turn's trace. Large event payloads live
// in their own files and are NOT fetched: the index already names the tool,
// and that is what a trace read is for.
func (h *handlers) traceEvents(sessionID string, t agentstore.ConversationTurn) ([]conversationEvent, int) {
	raw := t.Events
	if t.HasTrace && t.TurnID != "" {
		data, err := os.ReadFile(h.layout.SessionThinking(sessionID, t.TurnID))
		if err == nil {
			var idx agentstore.TurnTraceIndex
			if json.Unmarshal(data, &idx) == nil {
				raw = raw[:0:0]
				for _, ev := range idx.Events {
					raw = append(raw, agentstore.TurnEvent{
						Type: ev.Type, ToolName: ev.ToolName, ToolInput: ev.ToolInput,
						IsError: ev.IsError, Text: ev.Text, At: ev.At,
					})
				}
			}
		}
	}
	out := make([]conversationEvent, 0, len(raw))
	for _, ev := range raw {
		if len(out) >= convMaxEventsTurn {
			return out, len(raw) - len(out)
		}
		in, _ := clip(ev.ToolInput, convEventTextCap)
		txt, _ := clip(ev.Text, convEventTextCap)
		out = append(out, conversationEvent{
			Type: ev.Type, Tool: ev.ToolName, Input: in, Text: txt,
			IsError: ev.IsError, At: fmtTime(ev.At),
		})
	}
	return out, 0
}

// senderLabel names a human the way a reader would: the display name, with
// the handle when there is one, and the platform id as a last resort — never
// nothing, since "who said this" is half of reading someone else's thread.
func senderLabel(s *agentstore.Sender) string {
	name := strings.TrimSpace(s.Name)
	handle := strings.TrimSpace(s.Handle)
	switch {
	case name != "" && handle != "":
		return name + " (@" + handle + ")"
	case name != "":
		return name
	case handle != "":
		return "@" + handle
	default:
		return strings.TrimSpace(s.ID)
	}
}

func (h *handlers) readTurns(sessionID string) ([]agentstore.ConversationTurn, error) {
	var turns []agentstore.ConversationTurn
	err := storage.ReadJSONL(h.layout.SessionConversation(sessionID), func(line []byte) bool {
		var t agentstore.ConversationTurn
		if json.Unmarshal(line, &t) == nil && t.Role != "" {
			turns = append(turns, t)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return turns, nil
}

func clip(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	// Cut on a rune boundary so the tail is not a broken character.
	cut := max
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n… (truncated)", true
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
