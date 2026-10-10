package teamlink

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeDir struct{ peers []Peer }

func (d *fakeDir) Peers(_ context.Context, owner string) ([]Peer, error) {
	var out []Peer
	for _, p := range d.peers {
		if p.OwnerID == owner {
			out = append(out, p)
		}
	}
	return out, nil
}

func (d *fakeDir) Get(_ context.Context, id string) (Peer, error) {
	for _, p := range d.peers {
		if p.ID == id {
			return p, nil
		}
	}
	return Peer{}, errors.New("not found")
}

// fakeTurns answers every turn with reply(agent, text). gate, when set,
// holds each turn until it is closed.
type fakeTurns struct {
	mu    sync.Mutex
	seen  []string
	reply func(Peer, string) string
	gate  chan struct{}
	hub   *Hub
	// nested, when set, runs inside the turn of the agent it names —
	// a teammate messaging onwards while it answers.
	nested map[string]func() error
	errs   []error
}

func (f *fakeTurns) Run(_ context.Context, agent Peer, text string) (string, string, error) {
	f.mu.Lock()
	f.seen = append(f.seen, agent.Handle+" <- "+text)
	f.mu.Unlock()
	if f.gate != nil {
		<-f.gate
	}
	if fn := f.nested[agent.ID]; fn != nil {
		err := fn()
		f.mu.Lock()
		f.errs = append(f.errs, err)
		f.mu.Unlock()
	}
	return "sess-" + agent.ID, f.reply(agent, text), nil
}

type fakeNotify struct {
	mu        sync.Mutex
	delivered []string
	audits    []string
}

func (n *fakeNotify) Deliver(_ context.Context, sessionID, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.delivered = append(n.delivered, sessionID+": "+text)
	return nil
}

func (n *fakeNotify) Audit(_ context.Context, sessionID string, h Handoff) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.audits = append(n.audits, sessionID+" "+h.From+"->"+h.To+" "+stateName(h.State))
}

func (n *fakeNotify) snapshot() ([]string, []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.delivered...), append([]string(nil), n.audits...)
}

func newTestHub(reply func(Peer, string) string) (*Hub, *fakeTurns, *fakeNotify) {
	dir := &fakeDir{peers: []Peer{
		{ID: "a-cap", OwnerID: "u1", Handle: "captain", Name: "Yoga Bot", IsCaptain: true},
		{ID: "a-anton", OwnerID: "u1", Handle: "anton", Name: "Anton", Description: "reads logs"},
		{ID: "a-vera", OwnerID: "u1", Handle: "vera", Name: "Vera"},
		{ID: "a-off", OwnerID: "u1", Handle: "sleepy", Name: "Sleepy", Disabled: true},
		{ID: "a-other", OwnerID: "u2", Handle: "mallory", Name: "Mallory"},
	}}
	turns := &fakeTurns{reply: reply}
	note := &fakeNotify{}
	h := NewHub(dir, turns, note)
	h.poll = 5 * time.Millisecond
	turns.hub = h
	return h, turns, note
}

func TestSendSyncCaptainToAnton(t *testing.T) {
	h, turns, note := newTestHub(func(p Peer, _ string) string { return "no 401s since 09:00" })
	res, err := h.Send(context.Background(), SendInput{
		CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "@anton", Text: "any 401s today?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "completed" || res.ReplyText != "no 401s since 09:00" || res.ContextID == "" || res.TaskID == "" {
		t.Fatalf("result = %+v", res)
	}
	if got := turns.seen[0]; got != "anton <- Message from Yoga Bot (@captain):\nany 401s today?" {
		t.Fatalf("framed = %q", got)
	}
	_, audits := note.snapshot()
	// working is audited before the turn runs; fakeTurns is no
	// SessionLocator, so only the caller's side knows its session then.
	if len(audits) != 3 || audits[0] != "sess-cap captain->anton working" ||
		audits[1] != "sess-cap captain->anton completed" || audits[2] != "sess-a-anton captain->anton completed" {
		t.Fatalf("audits = %v", audits)
	}
	t.Logf("trace: %s | result %+v | audits %v", turns.seen[0], *res, audits)

	got, err := h.GetTask(context.Background(), "a-cap", res.TaskID)
	if err != nil || got.State != "completed" || got.ReplyText != res.ReplyText {
		t.Fatalf("get_task = %+v, %v", got, err)
	}
	if _, err := h.GetTask(context.Background(), "a-vera", res.TaskID); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("another agent read the task: %v", err)
	}
}

func TestSendRefusals(t *testing.T) {
	h, turns, _ := newTestHub(func(Peer, string) string { return "x" })
	ctx := context.Background()
	cases := map[string]struct {
		in   SendInput
		want error
	}{
		"other owner": {SendInput{CallerAgentID: "a-cap", To: "mallory", Text: "hi"}, ErrUnknownHandle},
		"disabled":    {SendInput{CallerAgentID: "a-cap", To: "sleepy", Text: "hi"}, ErrUnknownHandle},
		"self":        {SendInput{CallerAgentID: "a-cap", To: "@Captain", Text: "hi"}, ErrSelf},
		"no agent":    {SendInput{To: "anton", Text: "hi"}, ErrNotTeamSession},
	}
	for name, c := range cases {
		if _, err := h.Send(ctx, c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	if len(turns.seen) != 0 {
		t.Fatalf("a refused message ran a turn: %v", turns.seen)
	}
	if _, _, err := h.Resolve(ctx, "u2", "anton"); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("resolved across owners: %v", err)
	}
	card, _, err := h.Resolve(ctx, "u1", "anton")
	if err != nil || card.Name != "Anton" || card.SupportedInterfaces[0].URL != LocalURL("a-anton") {
		t.Fatalf("card = %+v, %v", card, err)
	}
}

func TestContextTurnLimit(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "ok" })
	ctx := context.Background()
	first, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "1"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= MaxContextTurns; i++ {
		if _, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "more", ContextID: first.ContextID}); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	_, err = h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "again", ContextID: first.ContextID})
	if !errors.Is(err, ErrHopLimit) || !strings.Contains(err.Error(), "summarise and report to the user") {
		t.Fatalf("err = %v", err)
	}
}

// A→B→A: Anton, answering the Captain, asks the Captain back; that turn
// shares the context and counts against it, and the chain depth grows.
func TestChainDepthAndPingPong(t *testing.T) {
	h, turns, _ := newTestHub(func(p Peer, _ string) string { return "from " + p.Handle })
	ctx := context.Background()
	var inner *Result
	turns.nested = map[string]func() error{
		"a-anton": func() (err error) {
			inner, err = h.Send(ctx, SendInput{CallerAgentID: "a-anton", To: "vera", Text: "help"})
			return err
		},
		"a-vera": func() error {
			_, err := h.Send(ctx, SendInput{CallerAgentID: "a-vera", To: "captain", Text: "back to you"})
			return err
		},
		"a-cap": func() error {
			_, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "fourth hop"})
			return err
		},
	}
	outer, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if inner == nil || inner.ContextID != outer.ContextID {
		t.Fatalf("nested send left the context: outer %+v inner %+v", outer, inner)
	}
	// anton→vera (depth 2) ok, vera→captain (depth 3) ok, captain→anton
	// would be depth 4. errs fill innermost first.
	if len(turns.errs) != 3 || !errors.Is(turns.errs[0], ErrHopLimit) || turns.errs[1] != nil || turns.errs[2] != nil {
		t.Fatalf("errs = %v", turns.errs)
	}
}

func TestSendAsyncDeliversLateReply(t *testing.T) {
	h, turns, note := newTestHub(func(Peer, string) string { return "done looking" })
	turns.gate = make(chan struct{})
	res, err := h.Send(context.Background(), SendInput{
		CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "slow job", Wait: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "working" || res.ReplyText != "" {
		t.Fatalf("result = %+v", res)
	}
	close(turns.gate)
	deadline := time.Now().Add(2 * time.Second)
	for {
		delivered, _ := note.snapshot()
		if len(delivered) == 1 {
			want := "sess-cap: Reply from Anton (@anton) [task " + res.TaskID + ", completed]:\n\ndone looking"
			if delivered[0] != want {
				t.Fatalf("delivered = %q", delivered[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("late reply never delivered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, err := h.GetTask(context.Background(), "a-cap", res.TaskID)
	if err != nil || got.State != "completed" || got.ReplyText != "done looking" {
		t.Fatalf("get_task = %+v, %v", got, err)
	}
}

func TestSilentReply(t *testing.T) {
	h, _, note := newTestHub(func(Peer, string) string { return " [silent] " })
	res, err := h.Send(context.Background(), SendInput{CallerSession: "s", CallerAgentID: "a-cap", To: "anton", Text: "fyi"})
	if err != nil || res.State != "completed" || res.ReplyText != "" {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if delivered, _ := note.snapshot(); len(delivered) != 0 {
		t.Fatalf("silent reply delivered: %v", delivered)
	}
}

// "@vera thanks" in Anton's reply continues the exchange it answered, so
// an @mention ping-pong runs into the same per-context cap as the tool.
func TestMentionAfterReplyContinuesContext(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "ok" })
	ctx := context.Background()
	first, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "q"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Send(ctx, SendInput{CallerAgentID: "a-anton", To: "captain", Text: "thanks", Mention: true, Wait: -1})
	if err != nil || res.ContextID != first.ContextID {
		t.Fatalf("mention = %+v, %v (first ctx %s)", res, err, first.ContextID)
	}
	human, err := h.Send(ctx, SendInput{CallerAgentID: "a-anton", To: "captain", Text: "from the person", Mention: true, Human: true, Wait: -1})
	if err != nil || human.ContextID == first.ContextID {
		t.Fatalf("a person's mention reused the agents' context: %+v, %v", human, err)
	}
}

// A context_id is only honoured for an exchange the caller's owner
// opened, and inside a chain it cannot reset the chain's budget.
func TestContextIDIsChecked(t *testing.T) {
	h, turns, _ := newTestHub(func(Peer, string) string { return "ok" })
	ctx := context.Background()
	for name, id := range map[string]string{"made up": "random-123", "empty-ish": "x"} {
		if _, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "hi", ContextID: id}); !errors.Is(err, ErrUnknownContext) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// Another owner's exchange.
	h.Dir.(*fakeDir).peers = append(h.Dir.(*fakeDir).peers, Peer{ID: "a-eve", OwnerID: "u2", Handle: "eve"})
	theirs, err := h.Send(ctx, SendInput{CallerAgentID: "a-other", To: "eve", Text: "private"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "join", ContextID: theirs.ContextID}); !errors.Is(err, ErrUnknownContext) {
		t.Fatalf("joined another owner's context: %v", err)
	}
	// Own exchange: fine.
	mine, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "q2", ContextID: mine.ContextID}); err != nil {
		t.Fatalf("own context refused: %v", err)
	}

	// Inside a chain, a fresh context_id is ignored for the chain's own.
	var inner *Result
	turns.nested = map[string]func() error{"a-anton": func() (err error) {
		inner, err = h.Send(ctx, SendInput{CallerAgentID: "a-anton", To: "vera", Text: "dodge", ContextID: mine.ContextID})
		return err
	}}
	outer, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if inner == nil || inner.ContextID != outer.ContextID {
		t.Fatalf("chain escaped its context: outer %s inner %+v", outer.ContextID, inner)
	}
}

func TestSentFromListsSessionTasks(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "done" })
	res, err := h.Send(context.Background(), SendInput{
		CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "\n  check the 401s\nmore detail",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := h.SentFrom("sess-cap")
	if len(got) != 1 {
		t.Fatalf("tasks = %+v", got)
	}
	v := got[0]
	if v.TaskID != res.TaskID || v.ToHandle != "anton" || v.ToID != "a-anton" || v.ToName != "Anton" ||
		v.Title != "check the 401s" || v.State != "completed" || v.Turns != 1 || v.MaxTurns != MaxContextTurns || v.Started.IsZero() {
		t.Fatalf("view = %+v", v)
	}
	if other := h.SentFrom("sess-other"); len(other) != 0 {
		t.Fatalf("another session saw the task: %+v", other)
	}
	if none := h.SentFrom(""); none != nil {
		t.Fatalf("empty session = %+v", none)
	}
}

type refusalNotify struct {
	fakeNotify
	refusals []Refusal
}

func (n *refusalNotify) Refused(_ context.Context, r Refusal) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.refusals = append(n.refusals, r)
}

// A refused mention and an exhausted budget are both reported to a
// Notifier that records refusals, in the caller's session.
func TestRefusalsReachNotifier(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "ok" })
	note := &refusalNotify{}
	h.Notify = note
	ctx := context.Background()
	// A non-mention refusal (the team_message tool) is the tool's error only.
	_, _ = h.Send(ctx, SendInput{CallerSession: "s-cap", CallerAgentID: "a-cap", To: "sleepy", Text: "hi"})
	_, _ = h.Send(ctx, SendInput{CallerSession: "s-cap", CallerAgentID: "a-cap", To: "@sleepy", Text: "hi", Mention: true, Wait: -1})
	first, err := h.Send(ctx, SendInput{CallerSession: "s-cap", CallerAgentID: "a-cap", To: "anton", Text: "1"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= MaxContextTurns+1; i++ {
		_, _ = h.Send(ctx, SendInput{CallerSession: "s-cap", CallerAgentID: "a-cap", To: "anton", Text: "more", ContextID: first.ContextID})
	}
	note.mu.Lock()
	defer note.mu.Unlock()
	if len(note.refusals) != 2 {
		t.Fatalf("refusals = %+v", note.refusals)
	}
	if r := note.refusals[0]; r.HopLimit || r.To != "sleepy" || r.From != "captain" || r.Session != "s-cap" || !errors.Is(r.Err, ErrUnknownHandle) {
		t.Fatalf("mention refusal = %+v", r)
	}
	if r := note.refusals[1]; !r.HopLimit || r.To != "anton" || r.ContextID != first.ContextID {
		t.Fatalf("hop limit = %+v", r)
	}
}

// TestRemotePeer: an A2A remote agent gets the mention text without the
// frame, and one kept to its owner refuses agents but not a person.
func TestRemotePeer(t *testing.T) {
	h, turns, _ := newTestHub(func(Peer, string) string { return "ok" })
	d := h.Dir.(*fakeDir)
	d.peers = append(d.peers,
		Peer{ID: "a-res", OwnerID: "u1", Handle: "research", Name: "Research", Remote: true},
		Peer{ID: "a-priv", OwnerID: "u1", Handle: "private", Name: "Private", Remote: true, RemoteOwnerOnly: true},
	)
	ctx := context.Background()
	if _, err := h.Send(ctx, SendInput{CallerSession: "s", CallerAgentID: "a-cap", To: "research", Text: "summarise RFC 9110"}); err != nil {
		t.Fatal(err)
	}
	if got := turns.seen[0]; got != "research <- summarise RFC 9110" {
		t.Fatalf("remote got %q", got)
	}
	if _, err := h.Send(ctx, SendInput{CallerSession: "s", CallerAgentID: "a-cap", To: "private", Text: "hi", Mention: true}); !errors.Is(err, ErrRemoteOwnerOnly) {
		t.Fatalf("owner-only remote: %v", err)
	}
	if _, err := h.Send(ctx, SendInput{CallerSession: "s", CallerAgentID: "a-cap", To: "private", Text: "hi", Human: true}); err != nil {
		t.Fatalf("person's mention refused: %v", err)
	}
	if (Peer{Remote: true, RemoteOwnerOnly: true}).AcceptsFrom(Peer{IsCaptain: true}) {
		t.Fatal("AcceptsFrom let an agent reach an owner-only remote")
	}
}

// TestRemoteNobodyExplains: a remote agent whose Mention is "Nobody"
// refuses even the Captain, and the error names the agent, the setting
// and the way out, so the Captain can explain instead of guessing.
func TestRemoteNobodyExplains(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "ok" })
	d := h.Dir.(*fakeDir)
	d.peers = append(d.peers, Peer{ID: "a-halo", OwnerID: "u1", Handle: "halodev", Name: "Halodev", Remote: true, MentionFrom: MentionOff})
	_, err := h.Send(context.Background(), SendInput{CallerSession: "s", CallerAgentID: "a-cap", To: "halodev", Text: "any commit today?"})
	if !errors.Is(err, ErrRemoteOwnerOnly) {
		t.Fatalf("err = %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"@halodev", "Nobody", "Settings › Mention", "Any of my agents"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "A2A") {
		t.Fatalf("error still names A2A: %q", msg)
	}
}

// A wait past QuickWait is clamped, so the call hands back state=working
// quickly instead of holding the turn, and the reply still lands.
func TestSendWaitClampedToQuickWait(t *testing.T) {
	if QuickWait > 15*time.Second {
		t.Fatalf("QuickWait %s must stay short", QuickWait)
	}
	h, turns, note := newTestHub(func(Peer, string) string { return "late answer" })
	h.maxWait = 20 * time.Millisecond
	turns.gate = make(chan struct{})
	start := time.Now()
	res, err := h.Send(context.Background(), SendInput{
		CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "slow job", Wait: 300 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "working" || time.Since(start) > 5*time.Second {
		t.Fatalf("result = %+v after %s", res, time.Since(start))
	}
	close(turns.gate)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if delivered, _ := note.snapshot(); len(delivered) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("late reply never delivered")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// openerTurns is fakeTurns that can open chats (ChatOpener).
type openerTurns struct {
	*fakeTurns
	opened []string
	ran    []string
}

func (c *openerTurns) NewChat(_ context.Context, agent Peer) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opened = append(c.opened, agent.Handle)
	return "chat-" + agent.Handle + "-" + string(rune('0'+len(c.opened))), nil
}

func (c *openerTurns) RunIn(ctx context.Context, agent Peer, sessionID, text string) (string, string, error) {
	c.mu.Lock()
	c.ran = append(c.ran, agent.Handle+"@"+sessionID)
	c.mu.Unlock()
	_, reply, err := c.Run(ctx, agent, text)
	return sessionID, reply, err
}

func TestNewChatOpensAndKeepsAChat(t *testing.T) {
	h, turns, _ := newTestHub(func(Peer, string) string { return "ok" })
	h.Dir.(*fakeDir).peers = append(h.Dir.(*fakeDir).peers,
		Peer{ID: "a-res", OwnerID: "u1", Handle: "research", Name: "Research", Remote: true})
	ct := &openerTurns{fakeTurns: turns}
	h.Turns = ct
	ctx := context.Background()
	for _, to := range []string{"anton", "research"} {
		first, err := h.Send(ctx, SendInput{CallerSession: "s", CallerAgentID: "a-cap", To: to, Text: "fresh start", NewChat: true})
		if err != nil || first.State != "completed" {
			t.Fatalf("%s new_chat: %+v, %v", to, first, err)
		}
		// The same exchange stays in the chat new_chat opened.
		if _, err := h.Send(ctx, SendInput{CallerSession: "s", CallerAgentID: "a-cap", To: to, Text: "and then", ContextID: first.ContextID}); err != nil {
			t.Fatal(err)
		}
		// A fresh exchange goes back to the main chat.
		if _, err := h.Send(ctx, SendInput{CallerSession: "s", CallerAgentID: "a-cap", To: to, Text: "main again"}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(ct.opened, ",") != "anton,research" {
		t.Fatalf("opened = %v", ct.opened)
	}
	if got := strings.Join(ct.ran, ","); got != "anton@chat-anton-1,anton@chat-anton-1,research@chat-research-2,research@chat-research-2" {
		t.Fatalf("ran in = %s", got)
	}
	if len(turns.seen) != 6 {
		t.Fatalf("turns = %v", turns.seen)
	}
}

func TestNewChatUnsupported(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "ok" })
	_, err := h.Send(context.Background(), SendInput{CallerSession: "s", CallerAgentID: "a-cap", To: "anton", Text: "hi", NewChat: true})
	if !errors.Is(err, ErrNewChatUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestFollowUpReachesTheAsker(t *testing.T) {
	h, _, note := newTestHub(func(p Peer, _ string) string { return "first answer" })
	if _, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "@anton", Text: "check"}); err != nil {
		t.Fatal(err)
	}
	if !h.FollowUp(context.Background(), "sess-a-anton", "one more thing") {
		t.Fatal("follow-up not delivered")
	}
	delivered, _ := note.snapshot()
	last := delivered[len(delivered)-1]
	if !strings.HasPrefix(last, "sess-cap: Follow-up from ") || !strings.HasSuffix(last, "one more thing") {
		t.Fatalf("delivered = %q", delivered)
	}
	if h.FollowUp(context.Background(), "sess-unknown", "x") {
		t.Fatal("delivered for a session that answered nothing")
	}
}
