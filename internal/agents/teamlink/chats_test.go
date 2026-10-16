package teamlink

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// listTurns adds ChatLister to linkTurns: every chat in the store of the
// agent's side, owned by its chat user, plus foreign chats (other people's
// sessions the lister must never let through).
type listTurns struct {
	*linkTurns
	foreign []ChatInfo
}

func (l *listTurns) Chats(_ context.Context, agent Peer) []ChatInfo {
	l.st.mu.Lock()
	defer l.st.mu.Unlock()
	var out []ChatInfo
	for id, r := range l.st.chats {
		if r.key == chatKey(agent) {
			// Opened later = active later.
			n, _ := strconv.Atoi(id[strings.LastIndex(id, "-")+1:])
			out = append(out, ChatInfo{SessionID: id, Title: "t-" + id, UserID: agent.chatUser(), LinkedFrom: r.from, LastActive: time.Unix(int64(n), 0)})
		}
	}
	return append(out, l.foreign...)
}

// PairedWith answers from Chats over every agent, as the registry scan
// does in wiring: one pass, foreign chats included (LinkedChats filters).
func (l *listTurns) PairedWith(_ context.Context, caller string) []ChatInfo {
	l.st.mu.Lock()
	var out []ChatInfo
	for id, r := range l.st.chats {
		if r.from == caller {
			agentID, user, _ := strings.Cut(r.key, "\x00")
			if user == "" {
				user = l.ownerOf(agentID)
			}
			out = append(out, ChatInfo{SessionID: id, Title: "t-" + id, UserID: user, LinkedFrom: r.from, AgentID: agentID})
		}
	}
	l.st.mu.Unlock()
	for _, c := range l.foreign {
		if c.LinkedFrom == caller {
			out = append(out, c)
		}
	}
	return out
}

// ownerOf reads the owner from the test ids ("b-…" bob, "a-…" alice,
// "c-…" carol).
func (l *listTurns) ownerOf(agentID string) string {
	return map[byte]string{'b': "bob", 'a': "alice", 'c': "carol"}[agentID[0]]
}

func newListHub(st *linkStore, foreign ...ChatInfo) (*Hub, *listTurns) {
	h, _, lt, _ := newLinkHub(st)
	turns := &listTurns{linkTurns: lt, foreign: foreign}
	h.Turns = turns
	return h, turns
}

// P13 scope: a conversation sees only its own pairings, for teammates it
// can reach — never another conversation's, and never another user's chat
// (a shared agent's owner chat stays hidden whatever its pairing says).
func TestLinkedChatsOwnPairingsOnly(t *testing.T) {
	st := &linkStore{chats: map[string]linkRec{}}
	// alice's own chat with lena, paired (somehow) with bob's session: it
	// is alice's, so bob's conversation must not list it.
	h, turns := newListHub(st, ChatInfo{SessionID: "alice-lena", UserID: "alice", LinkedFrom: "s-1"})
	ctx := context.Background()
	for _, in := range []SendInput{
		{CallerSession: "s-1", CallerAgentID: "b-cap", To: "@anton", Text: "hi"},
		{CallerSession: "s-2", CallerAgentID: "b-cap", To: "@anton", Text: "hi"},
		{CallerSession: "s-a", CallerAgentID: "a-cap", To: "@kim", Text: "hi"},
	} {
		if _, err := h.Send(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	got, err := h.LinkedChats(ctx, "b-cap", "s-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Handle != "anton" || got[0].AgentID != "b-anton" || got[0].Chat == nil ||
		got[0].Chat.SessionID != turns.opened[0] || !got[0].Chat.Current {
		t.Fatalf("s-1 linked = %+v (opened %v)", got, turns.opened)
	}
	if got, _ := h.LinkedChats(ctx, "b-cap", "s-2", ""); len(got) != 1 || got[0].Chat.SessionID != turns.opened[1] {
		t.Fatalf("s-2 linked = %+v", got)
	}
	// alice's kim is not reachable for bob, and nothing of alice leaks.
	if got, _ := h.LinkedChats(ctx, "b-cap", "s-a", ""); len(got) != 0 {
		t.Fatalf("other user's pairing leaked: %+v", got)
	}
	rc, err := h.RecentChats(ctx, "b-cap", "s-1", "", "@lena")
	if err != nil {
		t.Fatal(err)
	}
	if rc.Chat != nil || len(rc.Recent) != 0 {
		t.Fatalf("lena recent = %+v, want none of alice's chats", rc)
	}
	// kim is alice's, not shared with bob: refused.
	if _, err := h.RecentChats(ctx, "b-cap", "s-1", "", "@kim"); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("unreachable kim: %v", err)
	}
}

// The dispatch result names the target chat it ran in.
func TestSendResultNamesChat(t *testing.T) {
	h, turns := newListHub(nil)
	res, err := h.Send(context.Background(), SendInput{CallerSession: "s-1", CallerAgentID: "b-cap", To: "@anton", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Chat == nil || res.Chat.SessionID != turns.opened[0] || res.Chat.Title == "" {
		t.Fatalf("result chat = %+v, opened %v", res.Chat, turns.opened)
	}
}

// Resume is explicit: a new conversation still gets a new chat; only a
// chat id the caller passes moves it into an older chat, which is then
// re-paired. Someone else's chat or chat+new_chat is refused.
func TestExplicitResume(t *testing.T) {
	h, turns := newListHub(nil, ChatInfo{SessionID: "alice-anton", UserID: "alice"})
	ctx := context.Background()
	send := func(in SendInput) (*Result, error) {
		in.CallerAgentID, in.To, in.Text = "b-cap", "@anton", "hi"
		return h.Send(ctx, in)
	}
	if _, err := send(SendInput{CallerSession: "s-1"}); err != nil {
		t.Fatal(err)
	}
	old := turns.opened[0]
	if _, err := send(SendInput{CallerSession: "s-2"}); err != nil {
		t.Fatal(err)
	}
	if len(turns.opened) != 2 || turns.opened[1] == old {
		t.Fatalf("a new conversation must get a new chat: %v", turns.opened)
	}
	res, err := send(SendInput{CallerSession: "s-2", Chat: old})
	if err != nil {
		t.Fatal(err)
	}
	if runs := turns.runs(); runs[2] != "anton/bob@"+old || res.Chat == nil || res.Chat.SessionID != old {
		t.Fatalf("resume ran %v, result %+v", runs, res.Chat)
	}
	if got, _ := h.LinkedChats(ctx, "b-cap", "s-2", ""); len(got) != 1 || got[0].Chat.SessionID != old {
		t.Fatalf("s-2 not re-paired: %+v", got)
	}
	// Later messages from s-2 stay in the resumed chat.
	if _, err := send(SendInput{CallerSession: "s-2"}); err != nil {
		t.Fatal(err)
	}
	if runs := turns.runs(); runs[3] != runs[2] || len(turns.opened) != 2 {
		t.Fatalf("after resume: %v opened %v", runs, turns.opened)
	}
	if _, err := send(SendInput{CallerSession: "s-2", Chat: "alice-anton"}); !errors.Is(err, ErrUnknownChat) {
		t.Fatalf("someone else's chat: %v", err)
	}
	if _, err := send(SendInput{CallerSession: "s-2", Chat: "nope"}); !errors.Is(err, ErrUnknownChat) {
		t.Fatalf("unknown chat: %v", err)
	}
	if _, err := send(SendInput{CallerSession: "s-2", Chat: old, NewChat: true}); !errors.Is(err, ErrChatAndNewChat) {
		t.Fatalf("chat+new_chat: %v", err)
	}
}

// RecentChats caps at MaxRecentChats, newest first, and marks the chat
// paired with the asking conversation.
func TestRecentChatsCapAndCurrent(t *testing.T) {
	h, turns := newListHub(nil)
	ctx := context.Background()
	for i := 0; i < MaxRecentChats+2; i++ {
		if _, err := h.Send(ctx, SendInput{CallerSession: fmt.Sprintf("s-%d", i), CallerAgentID: "b-cap", To: "@anton", Text: "hi"}); err != nil {
			t.Fatal(err)
		}
	}
	rc, err := h.RecentChats(ctx, "b-cap", "s-0", "", "anton")
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.Recent) != MaxRecentChats || rc.Chat == nil || rc.Chat.SessionID != turns.opened[0] || !rc.Chat.Current {
		t.Fatalf("recent = %+v", rc)
	}
	for i := 1; i < len(rc.Recent); i++ {
		if rc.Recent[i].LastActive.After(rc.Recent[i-1].LastActive) {
			t.Fatalf("not newest first: %+v", rc.Recent)
		}
	}
}

// The "This session" lines: nothing without a pairing; one short line per
// teammate with the Slack thread when there is one, and the resume rule.
func TestFormatLinkedChats(t *testing.T) {
	if got := FormatLinkedChats(nil); got != "" {
		t.Fatalf("empty = %q", got)
	}
	if got := FormatLinkedChats([]TeamChat{{Handle: "x"}}); got != "" {
		t.Fatalf("no chat = %q", got)
	}
	got := FormatLinkedChats([]TeamChat{
		{Handle: "anton", Chat: &ChatInfo{SessionID: "c1", Title: "Anton"}},
		{Handle: "slacky", Chat: &ChatInfo{SessionID: "c2", SlackThread: "https://slack.com/archives/C1/p1"}},
	})
	for _, want := range []string{
		"linked_team_chats",
		"- @anton → session c1 \"Anton\"",
		"- @slacky → session c2 slack: https://slack.com/archives/C1/p1",
		"Never move to an older chat on your own",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if n := len(got); n > 400 {
		t.Fatalf("block too long (%d bytes)", n)
	}
}

// Gate decision (audit round 1): a resume never widens past a plain send.
// In bob's chat with lena (alice's agent shared with bob), lena — an
// agent — reaches alice's scope only: bob's own anton is neither listed
// nor resumable, while bob's own chat with alice's max (shared with bob)
// is; alice's own chat with max is refused (not bob's).
func TestResumeInRecipientSession(t *testing.T) {
	h, turns := newListHub(nil, ChatInfo{SessionID: "alice-max", UserID: "alice", AgentID: "a-max"})
	ctx := context.Background()
	human := SendInput{CallerSession: "s-lena-bob", CallerAgentID: "a-lena", SessionUser: "bob", Text: "hi", Human: true, Mention: true}
	// bob's mentions (P5): his own anton, and max as shared with him.
	human.To = "@anton"
	if _, err := h.Send(ctx, human); err != nil {
		t.Fatal(err)
	}
	bobAnton := turns.opened[0]
	human.To = "@max"
	if _, err := h.Send(ctx, human); err != nil {
		t.Fatal(err)
	}
	bobMax := turns.opened[1]

	// Lists: alice's anton (in alice's reach), with no chat of bob's own
	// anton; max lists bob's chat with it.
	rc, err := h.RecentChats(ctx, "a-lena", "s-lena-bob", "bob", "anton")
	if err != nil || rc.AgentID != "a-anton" {
		t.Fatalf("recent anton = %+v, %v", rc, err)
	}
	for _, c := range rc.Recent {
		if c.SessionID == bobAnton {
			t.Fatalf("bob's own anton chat listed: %+v", rc)
		}
	}
	if rc, err := h.RecentChats(ctx, "a-lena", "s-lena-bob", "bob", "max"); err != nil || rc.AgentID != "a-max" ||
		len(rc.Recent) != 1 || rc.Recent[0].SessionID != bobMax {
		t.Fatalf("recent max = %+v, %v", rc, err)
	}
	linked, _ := h.LinkedChats(ctx, "a-lena", "s-lena-bob", "bob")
	if len(linked) != 1 || linked[0].AgentID != "a-max" || linked[0].Chat.SessionID != bobMax {
		t.Fatalf("linked = %+v", linked)
	}

	agent := SendInput{CallerSession: "s-lena-bob", CallerAgentID: "a-lena", SessionUser: "bob", Text: "hi"}
	// bob's own anton is out of reach for alice's agent, chat or not.
	resume := agent
	resume.To, resume.Chat = "@anton", bobAnton
	if _, err := h.Send(ctx, resume); !errors.Is(err, ErrUnknownChat) {
		t.Fatalf("resume of bob's own anton: %v", err)
	}
	// bob's own chat with max (in alice's reach) resumes, in bob's chat.
	resume.To, resume.Chat = "@max", bobMax
	res, err := h.Send(ctx, resume)
	if err != nil {
		t.Fatal(err)
	}
	if runs := turns.runs(); runs[len(runs)-1] != "max/alice@"+bobMax || res.Chat == nil || res.Chat.SessionID != bobMax {
		t.Fatalf("resume ran %v, chat %+v", runs, res.Chat)
	}
	if gt, err := h.GetTask(ctx, "a-lena", res.TaskID); err != nil || gt.Chat == nil || gt.Chat.SessionID != bobMax {
		t.Fatalf("get_task = %+v, %v", gt, err)
	}
	// alice's own chat with max is not bob's: refused.
	resume.Chat = "alice-max"
	if _, err := h.Send(ctx, resume); !errors.Is(err, ErrUnknownChat) {
		t.Fatalf("alice's chat: %v", err)
	}
	// Without the session's person (alice's own session), bob's chat is
	// not alice's either.
	noUser := resume
	noUser.Chat, noUser.SessionUser = bobMax, ""
	if _, err := h.Send(ctx, noUser); !errors.Is(err, ErrUnknownChat) {
		t.Fatalf("owner scope: %v", err)
	}
}
