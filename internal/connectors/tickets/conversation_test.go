// Self-test: reading a ticket's conversations through the connector surface.
package tickets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentstore "github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/ticket"
)

// writeTurns lays down a conversation.jsonl for a session: n user/assistant
// pairs, numbered so a page can be identified by its contents.
func writeTurns(t *testing.T, l agentconfig.Layout, sessionID string, n int) {
	t.Helper()
	path := l.SessionConversation(sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	base := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	enc := json.NewEncoder(f)
	for i := 0; i < n; i++ {
		role, text := "user", "ask "+strconv.Itoa(i)
		if i%2 == 1 {
			role, text = "assistant", "answer "+strconv.Itoa(i)
		}
		turn := agentstore.ConversationTurn{
			Role:      role,
			Text:      text,
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		}
		if role == "user" {
			turn.Sender = &agentstore.Sender{ID: "U1", Name: "Yoga", Handle: "yoga", Channel: "slack"}
		}
		if err := enc.Encode(turn); err != nil {
			t.Fatal(err)
		}
	}
}

func mkTicketWithSessions(t *testing.T, h *handlers, l agentconfig.Layout, sessions ...string) ticket.Ticket {
	t.Helper()
	tk, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "Payment webhook failing"})
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range sessions {
		mkSession(t, l, sid, "p1")
		if err := ticket.AttachSession(l, "p1", tk.ID, sid); err != nil {
			t.Fatal(err)
		}
	}
	reloaded, err := ticket.Load(l, "p1", tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	return reloaded
}

func TestConversationsListsTheTicketsSessions(t *testing.T) {
	h, l := newTestHandlers(t)
	tk := mkTicketWithSessions(t, h, l, "s1", "s2")
	writeTurns(t, l, "s1", 4)
	writeTurns(t, l, "s2", 2)

	res := mustDispatch(t, h.conversations, ctxFor("s1", map[string]string{"ticket_id": tk.ID}))
	m := res.(map[string]any)
	if m["total"].(int) != 2 {
		t.Fatalf("total = %v, want 2", m["total"])
	}
	rows := m["conversations"].([]conversationView)
	byID := map[string]conversationView{}
	for _, r := range rows {
		byID[r.SessionID] = r
	}
	if byID["s1"].Messages != 4 || byID["s2"].Messages != 2 {
		t.Fatalf("message counts = %d/%d, want 4/2", byID["s1"].Messages, byID["s2"].Messages)
	}
	// The caller's own conversation is marked, so an agent does not report
	// its own turns back as somebody else's prior work.
	if !byID["s1"].Current || byID["s2"].Current {
		t.Fatalf("is_current wrong: s1=%v s2=%v", byID["s1"].Current, byID["s2"].Current)
	}
}

// The default page is the TAIL: catching up on a conversation starts at the
// end, and the page itself still reads forwards.
func TestConversationReadDefaultsToTheNewestPage(t *testing.T) {
	h, l := newTestHandlers(t)
	tk := mkTicketWithSessions(t, h, l, "s1")
	writeTurns(t, l, "s1", 50)

	res := mustDispatch(t, h.conversationRead, ctxFor("", map[string]string{
		"project_id": "p1", "ticket_id": tk.ID, "session_id": "s1",
	}))
	m := res.(map[string]any)
	if m["total"].(int) != 50 || m["returned"].(int) != convDefaultLimit {
		t.Fatalf("total/returned = %v/%v, want 50/%d", m["total"], m["returned"], convDefaultLimit)
	}
	msgs := m["messages"].([]conversationMessage)
	if msgs[0].Index != 30 || msgs[len(msgs)-1].Index != 49 {
		t.Fatalf("page = [%d..%d], want [30..49]", msgs[0].Index, msgs[len(msgs)-1].Index)
	}
	if m["has_more"] != true || m["next_offset"].(int) != 20 {
		t.Fatalf("has_more/next_offset = %v/%v, want true/20", m["has_more"], m["next_offset"])
	}
	// final is the default, so no tool trace comes along.
	if m["detail"].(string) != "final" {
		t.Fatalf("detail = %v, want final", m["detail"])
	}
	for _, msg := range msgs {
		if len(msg.Events) != 0 {
			t.Fatalf("detail=final returned trace events on index %d", msg.Index)
		}
	}
	// The sender survives the round trip: who said it is half of reading
	// somebody else's thread.
	if msgs[0].Role == "user" && msgs[0].From != "Yoga (@yoga)" {
		t.Fatalf("from = %q, want \"Yoga (@yoga)\"", msgs[0].From)
	}
}

// next_offset walks BACKWARDS page by page without the caller computing it.
func TestConversationReadPagesBackwards(t *testing.T) {
	h, l := newTestHandlers(t)
	tk := mkTicketWithSessions(t, h, l, "s1")
	writeTurns(t, l, "s1", 50)

	res := mustDispatch(t, h.conversationRead, ctxFor("", map[string]string{
		"project_id": "p1", "ticket_id": tk.ID, "session_id": "s1",
		"limit": "10", "offset": "10",
	}))
	m := res.(map[string]any)
	msgs := m["messages"].([]conversationMessage)
	if msgs[0].Index != 30 || msgs[len(msgs)-1].Index != 39 {
		t.Fatalf("page = [%d..%d], want [30..39]", msgs[0].Index, msgs[len(msgs)-1].Index)
	}
}

func TestConversationReadOldestOrderStartsAtTheBeginning(t *testing.T) {
	h, l := newTestHandlers(t)
	tk := mkTicketWithSessions(t, h, l, "s1")
	writeTurns(t, l, "s1", 25)

	res := mustDispatch(t, h.conversationRead, ctxFor("", map[string]string{
		"project_id": "p1", "ticket_id": tk.ID, "session_id": "s1",
		"order": "oldest", "limit": "5",
	}))
	m := res.(map[string]any)
	msgs := m["messages"].([]conversationMessage)
	if msgs[0].Index != 0 || msgs[len(msgs)-1].Index != 4 {
		t.Fatalf("page = [%d..%d], want [0..4]", msgs[0].Index, msgs[len(msgs)-1].Index)
	}
	if m["next_offset"].(int) != 5 {
		t.Fatalf("next_offset = %v, want 5", m["next_offset"])
	}
}

// The last page says so rather than leaving a caller paging into nothing.
func TestConversationReadStopsAtTheEnd(t *testing.T) {
	h, l := newTestHandlers(t)
	tk := mkTicketWithSessions(t, h, l, "s1")
	writeTurns(t, l, "s1", 6)

	res := mustDispatch(t, h.conversationRead, ctxFor("", map[string]string{
		"project_id": "p1", "ticket_id": tk.ID, "session_id": "s1", "limit": "10",
	}))
	m := res.(map[string]any)
	if _, ok := m["has_more"]; ok {
		t.Fatalf("has_more set on a conversation that fits in one page: %v", m)
	}
	if m["returned"].(int) != 6 {
		t.Fatalf("returned = %v, want 6", m["returned"])
	}
}

// A session that is not on the ticket is refused: this op reads the work on a
// ticket, not any conversation on the install by id.
func TestConversationReadRefusesAForeignSession(t *testing.T) {
	h, l := newTestHandlers(t)
	tk := mkTicketWithSessions(t, h, l, "s1")
	mkSession(t, l, "other", "p1")
	writeTurns(t, l, "other", 3)

	_, err := h.conversationRead(ctxFor("", map[string]string{
		"project_id": "p1", "ticket_id": tk.ID, "session_id": "other",
	}))
	if err == nil {
		t.Fatal("expected a refusal for a session that is not attached to the ticket")
	}
}

// With several conversations on the ticket the op refuses to guess, and says
// where the list is.
func TestConversationReadNeedsSessionIDWhenAmbiguous(t *testing.T) {
	h, l := newTestHandlers(t)
	tk := mkTicketWithSessions(t, h, l, "s1", "s2")
	writeTurns(t, l, "s1", 2)
	writeTurns(t, l, "s2", 2)

	_, err := h.conversationRead(ctxFor("", map[string]string{"project_id": "p1", "ticket_id": tk.ID}))
	if err == nil {
		t.Fatal("expected an error when the ticket holds several conversations and none was named")
	}
}

func TestConversationReadTraceIncludesToolCalls(t *testing.T) {
	h, l := newTestHandlers(t)
	tk := mkTicketWithSessions(t, h, l, "s1")
	path := l.SessionConversation("s1")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	turn := agentstore.ConversationTurn{
		Role: "assistant", Text: "done", Timestamp: time.Now(),
		Events: []agentstore.TurnEvent{{Type: "tool_use", ToolName: "Bash", ToolInput: `{"command":"ls"}`}},
	}
	b, _ := json.Marshal(turn)
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	res := mustDispatch(t, h.conversationRead, ctxFor("", map[string]string{
		"project_id": "p1", "ticket_id": tk.ID, "session_id": "s1", "detail": "trace",
	}))
	m := res.(map[string]any)
	msgs := m["messages"].([]conversationMessage)
	if len(msgs) != 1 || len(msgs[0].Events) != 1 || msgs[0].Events[0].Tool != "Bash" {
		t.Fatalf("trace events = %+v, want one Bash tool_use", msgs)
	}
}

// A turn holding a whole generated document must not arrive whole.
func TestConversationReadClipsAHugeTurn(t *testing.T) {
	h, l := newTestHandlers(t)
	tk := mkTicketWithSessions(t, h, l, "s1")
	path := l.SessionConversation("s1")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	long := make([]byte, convTurnTextCap*3)
	for i := range long {
		long[i] = 'x'
	}
	b, _ := json.Marshal(agentstore.ConversationTurn{Role: "assistant", Text: string(long), Timestamp: time.Now()})
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	res := mustDispatch(t, h.conversationRead, ctxFor("", map[string]string{
		"project_id": "p1", "ticket_id": tk.ID, "session_id": "s1",
	}))
	msgs := res.(map[string]any)["messages"].([]conversationMessage)
	if !msgs[0].Truncated {
		t.Fatal("a turn over the cap must be marked truncated")
	}
	if len(msgs[0].Text) > convTurnTextCap+64 {
		t.Fatalf("clipped text is %d bytes, want ~%d", len(msgs[0].Text), convTurnTextCap)
	}
}
