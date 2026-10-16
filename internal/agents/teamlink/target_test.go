package teamlink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// targetRec is a session SessionTargeter knows: the agent it belongs to,
// whose it is and the ticket it is in ("" for none).
type targetRec struct{ agent, user, ticket string }

// targetTurns adds SessionTargeter and SessionLabeler to linkTurns over
// sessions that are no chat of any agent (Slack thread sessions); nested
// runs inside the turn of the session it names.
type targetTurns struct {
	*linkTurns
	sess   map[string]targetRec
	nested map[string]func() error
	mu     sync.Mutex
	texts  []string
	errs   []error
}

func (t *targetTurns) CanTarget(_ context.Context, agent Peer, caller, chat string) (bool, error) {
	r, ok := t.sess[chat]
	if !ok {
		return false, fmt.Errorf("session %s not found", chat)
	}
	if r.agent != agent.ID {
		return false, ErrUnknownChat
	}
	if c, ok := t.sess[caller]; ok && c.ticket != "" && c.ticket == r.ticket {
		return true, nil
	}
	if r.user == agent.chatUser() {
		return false, nil
	}
	return false, ErrUnknownChat
}

func (t *targetTurns) SessionLabel(_ context.Context, id string) string {
	return "session " + id + ", Slack thread https://slack.com/archives/C1/p1"
}

func (t *targetTurns) RunIn(ctx context.Context, agent Peer, sessionID, text string) (string, string, error) {
	t.mu.Lock()
	t.texts = append(t.texts, sessionID+": "+text)
	fn := t.nested[sessionID]
	t.mu.Unlock()
	if fn != nil {
		err := fn()
		t.mu.Lock()
		t.errs = append(t.errs, err)
		t.mu.Unlock()
	}
	return t.linkTurns.RunIn(ctx, agent, sessionID, text)
}

func newTargetHub(sess map[string]targetRec) (*Hub, *targetTurns, *linkStore) {
	st := &linkStore{chats: map[string]linkRec{}}
	h, _, lt, _ := newLinkHub(st)
	turns := &targetTurns{linkTurns: lt, sess: sess}
	h.Turns = turns
	return h, turns, st
}

// chat= reaches a session of the target that is none of its chats — its
// owner's Slack thread, or one in the asking chat's ticket — without
// pairing it; a missing session is an error, never a new chat.
func TestChatTargetsSessionOfTarget(t *testing.T) {
	h, turns, st := newTargetHub(map[string]targetRec{
		"slack-1":  {agent: "b-anton", user: "bob"},
		"slack-2":  {agent: "b-anton", user: "dave"},
		"slack-3":  {agent: "b-anton", user: "dave", ticket: "T1"},
		"cap-main": {agent: "b-cap", user: "bob", ticket: "T1"},
		"lena-1":   {agent: "a-lena", user: "alice"},
	})
	ctx := context.Background()
	send := func(caller, chat string) (*Result, error) {
		return h.Send(ctx, SendInput{CallerSession: caller, CallerAgentID: "b-cap", To: "@anton", Text: "hi", Chat: chat})
	}
	res, err := send("s-1", "slack-1")
	if err != nil {
		t.Fatal(err)
	}
	if runs := turns.runs(); len(runs) != 1 || runs[0] != "anton/bob@slack-1" || res.Chat == nil || res.Chat.SessionID != "slack-1" {
		t.Fatalf("ran %v, result chat %+v", runs, res.Chat)
	}
	if _, paired := st.chats["slack-1"]; paired {
		t.Fatal("a targeted session must not be paired with the caller")
	}
	if _, err := send("s-1", "slack-2"); !errors.Is(err, ErrUnknownChat) {
		t.Fatalf("another person's session outside a shared ticket: %v", err)
	}
	if _, err := send("cap-main", "slack-3"); err != nil {
		t.Fatalf("same ticket: %v", err)
	}
	if _, err := send("s-1", "nope"); err == nil || !strings.Contains(err.Error(), "session nope not found") {
		t.Fatalf("missing session: %v", err)
	}
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-1", CallerAgentID: "b-cap", To: "@lena", Text: "hi", Chat: "slack-1"}); !errors.Is(err, ErrUnknownChat) {
		t.Fatalf("a session of another agent: %v", err)
	}
	if len(turns.opened) != 0 {
		t.Fatalf("no chat may be opened: %v", turns.opened)
	}
}

// An agent reaches another chat of its own only within one ticket; the
// message names the chat it came from.
func TestSelfChatSameTicketOnly(t *testing.T) {
	h, turns, _ := newTargetHub(map[string]targetRec{
		"main":  {agent: "b-anton", user: "bob", ticket: "T1"},
		"card":  {agent: "b-anton", user: "bob", ticket: "T1"},
		"other": {agent: "b-anton", user: "bob"},
	})
	ctx := context.Background()
	send := func(chat string) (*Result, error) {
		return h.Send(ctx, SendInput{CallerSession: "main", CallerAgentID: "b-anton", To: "@anton", Text: "status?", Chat: chat})
	}
	res, err := send("card")
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "completed" || res.ReplyText != "ok" {
		t.Fatalf("reply did not come back: %+v", res)
	}
	if len(turns.texts) != 1 || !strings.Contains(turns.texts[0], "card: Message from your own other chat (session main, Slack thread https://slack.com/archives/C1/p1)") {
		t.Fatalf("framing = %q", turns.texts)
	}
	if _, err := send("other"); !errors.Is(err, ErrSelfChatTicket) {
		t.Fatalf("own chat outside the ticket: %v", err)
	}
	for _, chat := range []string{"", "main"} {
		if _, err := send(chat); !errors.Is(err, ErrSelf) {
			t.Fatalf("chat %q: %v", chat, err)
		}
	}
}

// A→A ping-pong between two chats of one agent runs on one chain: each
// turn deepens it, and the hop limit ends it.
func TestSelfChatPingPongHopLimit(t *testing.T) {
	h, turns, _ := newTargetHub(map[string]targetRec{
		"main": {agent: "b-anton", user: "bob", ticket: "T1"},
		"card": {agent: "b-anton", user: "bob", ticket: "T1"},
	})
	ctx := context.Background()
	to := func(from, chat string) func() error {
		return func() error {
			_, err := h.Send(ctx, SendInput{CallerSession: from, CallerAgentID: "b-anton", To: "@anton", Text: "back", Chat: chat})
			return err
		}
	}
	turns.nested = map[string]func() error{"card": to("card", "main"), "main": to("main", "card")}
	res, err := h.Send(ctx, SendInput{CallerSession: "main", CallerAgentID: "b-anton", To: "@anton", Text: "start", Chat: "card"})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "completed" {
		t.Fatalf("outer task = %+v", res)
	}
	turns.mu.Lock()
	defer turns.mu.Unlock()
	// card→main (2), main→card (3), card→main would be 4: errs fill
	// innermost first.
	if len(turns.errs) != 3 || !errors.Is(turns.errs[0], ErrHopLimit) || turns.errs[1] != nil || turns.errs[2] != nil {
		t.Fatalf("errs = %v", turns.errs)
	}
}

// A message to the agent's own chat in the same ticket goes through
// whoever the asking chat belongs to (a requester's Slack thread), and a
// missing session is refused, never opened.
func TestSelfChatAnySessionUserMissingSession(t *testing.T) {
	h, turns, _ := newTargetHub(map[string]targetRec{
		"main": {agent: "b-anton", user: "carol", ticket: "T1"},
		"card": {agent: "b-anton", user: "bob", ticket: "T1"},
	})
	ctx := context.Background()
	send := func(chat string) (*Result, error) {
		return h.Send(ctx, SendInput{CallerSession: "main", CallerAgentID: "b-anton", SessionUser: "carol", To: "@anton", Text: "status?", Chat: chat})
	}
	if res, err := send("card"); err != nil || res.State != "completed" {
		t.Fatalf("same ticket, asked from carol's session: %+v, %v", res, err)
	}
	if _, err := send("nope"); err == nil || !strings.Contains(err.Error(), "session nope not found") {
		t.Fatalf("missing session: %v", err)
	}
	if len(turns.opened) != 0 {
		t.Fatalf("no chat may be opened: %v", turns.opened)
	}
}

// The agent's own chat asking back (input_required) is answered with
// task_id, in the chat the exchange runs in.
func TestSelfChatAnswerInputRequired(t *testing.T) {
	h, turns, _ := newTargetHub(map[string]targetRec{
		"main": {agent: "b-anton", user: "bob", ticket: "T1"},
		"card": {agent: "b-anton", user: "bob", ticket: "T1"},
	})
	turns.asks = 1
	ctx := context.Background()
	res, err := h.Send(ctx, SendInput{CallerSession: "main", CallerAgentID: "b-anton", To: "@anton", Text: "status?", Chat: "card"})
	if err != nil || res.State != "input_required" {
		t.Fatalf("ask back: %+v, %v", res, err)
	}
	ans, err := h.Send(ctx, SendInput{CallerSession: "main", CallerAgentID: "b-anton", TaskID: res.TaskID, Text: "the first"})
	if err != nil {
		t.Fatal(err)
	}
	if ans.State != "completed" || ans.ReplyText != "ok" {
		t.Fatalf("answer = %+v", ans)
	}
	if runs := turns.runs(); len(runs) != 2 || runs[1] != "anton/bob@card" {
		t.Fatalf("runs = %v", runs)
	}
}
