package teamlink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// linkStore stands in for the target chats' session meta: which agent
// (chatKey) each chat belongs to and the conversation it is paired with.
// It outlives a Hub, as the meta on disk outlives a restart.
type linkStore struct {
	mu    sync.Mutex
	chats map[string]linkRec // chat id → record
	n     int
}

type linkRec struct{ key, from string }

// linkTurns is a Turns that opens, pairs and runs in chats (ChatOpener +
// ChatLinker), recording where each turn ran.
type linkTurns struct {
	st     *linkStore
	mu     sync.Mutex
	ran    []string
	opened []string
}

func (l *linkTurns) Run(_ context.Context, agent Peer, _ string) (string, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ran = append(l.ran, agent.Handle+"/"+agent.OwnerID+"@main:"+agent.ChatUser)
	return "main-" + agent.ID + "-" + agent.ChatUser, "ok", nil
}

func (l *linkTurns) RunIn(_ context.Context, agent Peer, sessionID, _ string) (string, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ran = append(l.ran, agent.Handle+"/"+agent.OwnerID+"@"+sessionID)
	return sessionID, "ok", nil
}

func (l *linkTurns) NewChat(_ context.Context, agent Peer) (string, error) {
	l.st.mu.Lock()
	defer l.st.mu.Unlock()
	l.st.n++
	id := fmt.Sprintf("chat-%s-%s-%d", agent.Handle, agent.ChatUser, l.st.n)
	l.st.chats[id] = linkRec{key: chatKey(agent)}
	l.mu.Lock()
	l.opened = append(l.opened, id)
	l.mu.Unlock()
	return id, nil
}

func (l *linkTurns) LinkedChat(_ context.Context, agent Peer, caller string) string {
	l.st.mu.Lock()
	defer l.st.mu.Unlock()
	for id, r := range l.st.chats {
		if r.key == chatKey(agent) && r.from == caller {
			return id
		}
	}
	if r, ok := l.st.chats[caller]; ok && r.from != "" {
		if back, ok := l.st.chats[r.from]; ok && back.key == chatKey(agent) {
			return r.from
		}
	}
	return ""
}

func (l *linkTurns) Link(_ context.Context, agent Peer, chat, caller string) error {
	l.st.mu.Lock()
	defer l.st.mu.Unlock()
	for id, r := range l.st.chats {
		if r.key == chatKey(agent) && r.from == caller {
			r.from = ""
			l.st.chats[id] = r
		}
	}
	r := l.st.chats[chat]
	r.from = caller
	l.st.chats[chat] = r
	return nil
}

func (l *linkTurns) runs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ran...)
}

// newLinkHub: bob (the recipient) owns captain and anton; alice (the
// sharing owner) owns lena, anton, max (shared with bob) and kim (not
// shared); carol owns zed, shared with alice only.
func newLinkHub(st *linkStore) (*Hub, *sharedDir, *linkTurns, *refusalNotify) {
	dir := &sharedDir{
		fakeDir: fakeDir{peers: []Peer{
			{ID: "b-cap", OwnerID: "bob", Handle: "captain", IsCaptain: true},
			{ID: "b-anton", OwnerID: "bob", Handle: "anton"},
			{ID: "a-cap", OwnerID: "alice", Handle: "captain", IsCaptain: true},
			{ID: "a-lena", OwnerID: "alice", Handle: "lena"},
			{ID: "a-anton", OwnerID: "alice", Handle: "anton"},
			{ID: "a-max", OwnerID: "alice", Handle: "max"},
			{ID: "a-kim", OwnerID: "alice", Handle: "kim"},
			{ID: "c-zed", OwnerID: "carol", Handle: "zed"},
		}},
		shared: map[string][]string{
			"bob":   {"a-lena", "a-anton", "a-max"},
			"alice": {"c-zed"},
		},
	}
	if st == nil {
		st = &linkStore{chats: map[string]linkRec{}}
	}
	turns := &linkTurns{st: st}
	note := &refusalNotify{}
	h := NewHub(dir, turns, note)
	h.poll = 5 * time.Millisecond
	return h, dir, turns, note
}

func handles(ps []Peer) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Handle+":"+p.OwnerID+":"+p.ChatUser)
	}
	return strings.Join(out, ",")
}

// P4: bob's own agents may message an agent alice shared with bob; the
// turn runs in bob's chat with it.
func TestOwnAgentMessagesAgentSharedWithOwner(t *testing.T) {
	h, _, turns, _ := newLinkHub(nil)
	ctx := context.Background()
	res, err := h.Send(ctx, SendInput{CallerSession: "s-bcap", CallerAgentID: "b-cap", To: "@lena", Text: "hi"})
	if err != nil || res.State != "completed" {
		t.Fatalf("team_message = %+v, %v", res, err)
	}
	// An @mention in the agent's own reply goes through as well.
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-banton", CallerAgentID: "b-anton", To: "@lena", Text: "hi", Mention: true}); err != nil {
		t.Fatal(err)
	}
	runs := turns.runs()
	if len(runs) != 2 || !strings.HasPrefix(runs[0], "lena/alice@chat-lena-bob-") || !strings.HasPrefix(runs[1], "lena/alice@chat-lena-bob-") {
		t.Fatalf("runs = %v", runs)
	}
	// The roster (and so the tool's visibility) carries the shared agent;
	// bob's own anton wins the handle over alice's.
	peers, err := h.Reachable(ctx, "b-cap")
	if err != nil {
		t.Fatal(err)
	}
	if got := handles(peers); got != "anton:bob:,lena:alice:bob,max:alice:bob" {
		t.Fatalf("reachable = %s", got)
	}
}

func TestAgentOfThirdOwnerRefused(t *testing.T) {
	h, dir, turns, _ := newLinkHub(nil)
	ctx := context.Background()
	// carol's zed is shared with alice, not bob: bob's agents never see it.
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-bcap", CallerAgentID: "b-cap", To: "@zed", Text: "hi"}); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("third owner = %v, want ErrUnknownHandle", err)
	}
	// alice's unshared kim neither.
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-bcap", CallerAgentID: "b-cap", To: "@kim", Text: "hi"}); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("unshared = %v, want ErrUnknownHandle", err)
	}
	// The shared agent's mention policy still applies to bob's agents.
	dir.mu.Lock()
	for i := range dir.peers {
		if dir.peers[i].ID == "a-lena" {
			dir.peers[i].MentionFrom = MentionCaptain
		}
	}
	dir.mu.Unlock()
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-banton", CallerAgentID: "b-anton", To: "@lena", Text: "hi"}); !errors.Is(err, ErrMentionsOff) {
		t.Fatalf("policy = %v, want ErrMentionsOff", err)
	}
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-bcap", CallerAgentID: "b-cap", To: "@lena", Text: "hi"}); err != nil {
		t.Fatalf("captain refused: %v", err)
	}
	if runs := turns.runs(); len(runs) != 1 {
		t.Fatalf("runs = %v", runs)
	}
}

// rawSend hands targetID a message with meta as if a Hub had sent it.
func rawSend(t *testing.T, h *Hub, targetID string, meta map[string]any) a2a.TaskState {
	t.Helper()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))
	msg.ContextID = "ctx-raw"
	msg.Metadata = meta
	res, err := h.handler(targetID).SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatal(err)
	}
	task, ok := res.(*a2a.Task)
	if !ok {
		t.Fatalf("result = %T", res)
	}
	return task.Status.State
}

// The executor takes no cross-owner message the shares do not back,
// whatever the metadata claims.
func TestExecutorRechecksCrossOwner(t *testing.T) {
	h, dir, turns, _ := newLinkHub(nil)
	cases := map[string]struct {
		target string
		meta   map[string]any
	}{
		"third owner as chat user":  {"a-lena", map[string]any{metaFrom: "c-zed", metaChatUser: "carol"}},
		"unshared agent":            {"a-kim", map[string]any{metaFrom: "b-cap", metaChatUser: "bob"}},
		"session user not shared":   {"b-cap", map[string]any{metaFrom: "a-kim", metaSessionUser: "bob"}},
		"session user, owner's kim": {"a-kim", map[string]any{metaFrom: "a-lena", metaSessionUser: "bob", metaChatUser: "bob"}},
		"session user, wrong chat":  {"b-cap", map[string]any{metaFrom: "a-lena", metaSessionUser: "bob", metaChatUser: "alice"}},
	}
	for name, c := range cases {
		if st := rawSend(t, h, c.target, c.meta); st != a2a.TaskStateFailed {
			t.Errorf("%s: state = %s, want failed", name, st)
		}
	}
	// Unshared since the mention was sent: refused too.
	dir.mu.Lock()
	dir.shared["bob"] = []string{"a-max"}
	dir.mu.Unlock()
	if st := rawSend(t, h, "b-cap", map[string]any{metaFrom: "a-lena", metaSessionUser: "bob"}); st != a2a.TaskStateFailed {
		t.Errorf("after unshare: state = %s", st)
	}
	if runs := turns.runs(); len(runs) != 0 {
		t.Fatalf("a refused message ran: %v", runs)
	}
}

// P5: in bob's chat with alice's lena, bob's @mention resolves against
// bob — his own agents and those shared with him — never alice's others.
func TestMentionInSharedChatResolvesAgainstSessionUser(t *testing.T) {
	h, _, turns, note := newLinkHub(nil)
	ctx := context.Background()
	send := func(to string) error {
		_, err := h.Send(ctx, SendInput{CallerSession: "s-lena-bob", CallerAgentID: "a-lena", SessionUser: "bob", To: to, Text: "hi", Human: true, Mention: true})
		return err
	}
	for _, to := range []string{"@captain", "@anton", "@max"} {
		if err := send(to); err != nil {
			t.Fatalf("%s: %v", to, err)
		}
	}
	runs := turns.runs()
	want := []string{"captain/bob@chat-captain--", "anton/bob@chat-anton--", "max/alice@chat-max-bob-"}
	if len(runs) != len(want) {
		t.Fatalf("runs = %v", runs)
	}
	for i, w := range want {
		if !strings.HasPrefix(runs[i], w) {
			t.Fatalf("run %d = %s, want %s…", i, runs[i], w)
		}
	}
	// alice's unshared kim is unknown to bob: a refusal, no turn.
	if err := send("@kim"); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("@kim = %v, want ErrUnknownHandle", err)
	}
	note.mu.Lock()
	nref := len(note.refusals)
	note.mu.Unlock()
	if nref != 1 || len(turns.runs()) != 3 {
		t.Fatalf("refusals = %d, runs = %v", nref, turns.runs())
	}
	// The handles the router offers there are the same set.
	peers, err := h.MentionTargets(ctx, "a-lena", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if got := handles(peers); got != "captain:bob:,anton:bob:,max:alice:bob" {
		t.Fatalf("targets = %s", got)
	}
	// alice in her own chat with lena is unchanged: her own kim answers in
	// her chat.
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-lena-alice", CallerAgentID: "a-lena", SessionUser: "alice", To: "@kim", Text: "hi", Human: true, Mention: true}); err != nil {
		t.Fatal(err)
	}
	if last := turns.runs()[3]; !strings.HasPrefix(last, "kim/alice@chat-kim--") {
		t.Fatalf("owner run = %s", last)
	}
	if peers, _ := h.MentionTargets(ctx, "a-lena", "alice"); handles(peers) != "captain:alice:,anton:alice:,max:alice:,kim:alice:,zed:carol:alice" {
		t.Fatalf("owner targets = %s", handles(peers))
	}
}

// P6: each caller conversation gets one chat at the target, kept across a
// restart; new_chat opens a fresh one and re-pairs.
func TestChatFollowsCallerSession(t *testing.T) {
	st := &linkStore{chats: map[string]linkRec{}}
	h, _, turns, _ := newLinkHub(st)
	ctx := context.Background()
	send := func(h *Hub, from string, newChat bool) {
		t.Helper()
		if _, err := h.Send(ctx, SendInput{CallerSession: from, CallerAgentID: "b-cap", To: "@anton", Text: "hi", NewChat: newChat}); err != nil {
			t.Fatal(err)
		}
	}
	send(h, "s-1", false)
	send(h, "s-2", false)
	send(h, "s-1", false)
	runs := turns.runs()
	if runs[0] == runs[1] || runs[0] != runs[2] {
		t.Fatalf("two callers must get two chats, one caller one: %v", runs)
	}
	if len(turns.opened) != 2 {
		t.Fatalf("opened = %v", turns.opened)
	}

	// A fresh Hub (restart) reads the pair back.
	h2, _, turns2, _ := newLinkHub(st)
	send(h2, "s-1", false)
	if got := turns2.runs(); len(got) != 1 || got[0] != runs[0] || len(turns2.opened) != 0 {
		t.Fatalf("after restart: runs %v opened %v, want %s", got, turns2.opened, runs[0])
	}

	// new_chat opens a fresh chat, and later messages follow it.
	send(h2, "s-1", true)
	send(h2, "s-1", false)
	got := turns2.runs()
	if len(turns2.opened) != 1 || got[1] != "anton/bob@"+turns2.opened[0] || got[2] != got[1] {
		t.Fatalf("new_chat: runs %v opened %v", got, turns2.opened)
	}
}

// P6: a reply back to the asker lands in the conversation that asked, not
// a new chat of the asker.
func TestReplyBackLandsInAskingChat(t *testing.T) {
	st := &linkStore{chats: map[string]linkRec{"s-cap": {key: "b-cap\x00"}}}
	h, _, turns, _ := newLinkHub(st)
	ctx := context.Background()
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-cap", CallerAgentID: "b-cap", To: "@anton", Text: "check"}); err != nil {
		t.Fatal(err)
	}
	antonChat := turns.opened[0]
	if _, err := h.Send(ctx, SendInput{CallerSession: antonChat, CallerAgentID: "b-anton", To: "@captain", Text: "done"}); err != nil {
		t.Fatal(err)
	}
	if got := turns.runs(); got[1] != "captain/bob@s-cap" || len(turns.opened) != 1 {
		t.Fatalf("runs %v opened %v", got, turns.opened)
	}
}

// P6 with P5: a person's mention in a shared chat pairs a chat for that
// person, once.
func TestSharedMentionPairsRecipientChat(t *testing.T) {
	h, _, turns, _ := newLinkHub(nil)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := h.Send(ctx, SendInput{CallerSession: "s-lena-bob", CallerAgentID: "a-lena", SessionUser: "bob", To: "@max", Text: "hi", Human: true, Mention: true}); err != nil {
			t.Fatal(err)
		}
	}
	runs := turns.runs()
	if len(turns.opened) != 1 || runs[0] != runs[1] || !strings.HasPrefix(runs[0], "max/alice@chat-max-bob-") {
		t.Fatalf("runs %v opened %v", runs, turns.opened)
	}
}
