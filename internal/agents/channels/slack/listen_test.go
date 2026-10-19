package slack

import (
	"strings"
	"sync"
	"testing"
	"time"

	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// fakeListenSessions is a SessionChecker + ListenStore in memory.
type fakeListenSessions struct {
	mu       sync.Mutex
	auto     map[string]bool
	threads  map[string]agentchannels.ListenThread
	agentDoc string
	channels map[string]bool
}

func newFakeListen(agentDoc string) *fakeListenSessions {
	return &fakeListenSessions{auto: map[string]bool{}, threads: map[string]agentchannels.ListenThread{}, agentDoc: agentDoc, channels: map[string]bool{}}
}

func (f *fakeListenSessions) SessionExists(string) bool { return true }
func (f *fakeListenSessions) AutoReplyOn(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.auto[id]
}
func (f *fakeListenSessions) SetAutoReply(id string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auto[id] = on
}
func (f *fakeListenSessions) ThreadBinding(string) (agentchannels.ThreadBinding, bool) {
	return agentchannels.ThreadBinding{}, false
}
func (f *fakeListenSessions) SetThreadBinding(string, agentchannels.ThreadBinding) {}
func (f *fakeListenSessions) ListenThread(id string) agentchannels.ListenThread {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.threads[id]
}
func (f *fakeListenSessions) SetListenThread(id string, lt agentchannels.ListenThread) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threads[id] = lt
}
func (f *fakeListenSessions) ListenDocs(string) (string, string) { return "", f.agentDoc }
func (f *fakeListenSessions) ListenChannel(instance, chatID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.channels[instance+"/"+chatID]
}
func (f *fakeListenSessions) SetListenChannel(instance, chatID string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channels[instance+"/"+chatID] = on
}

func TestParseListenDoc(t *testing.T) {
	keys, body := parseListenDoc("---\ndelay: 2m   # wait\nscope: \"channel\"\n---\nOnly WABA.\n")
	if keys["delay"] != "2m" || keys["scope"] != "channel" || body != "Only WABA." {
		t.Fatalf("keys=%v body=%q", keys, body)
	}
	keys, body = parseListenDoc("Just text, no frontmatter.")
	if len(keys) != 0 || body != "Just text, no frontmatter." {
		t.Fatalf("plain doc: keys=%v body=%q", keys, body)
	}
}

// The agent's LISTEN.md wins key by key and adds its body after the
// default's; replace_default drops the default body; the thread's request
// comes last.
func TestResolveListenLayers(t *testing.T) {
	st := resolveListen("", "", agentchannels.ListenThread{})
	if st.Delay != 60*time.Second || st.Scope != "thread" || st.OnTeamReply != "review" || !strings.Contains(st.Instructions, "[silent]") {
		t.Fatalf("built-in default = %+v", st)
	}
	st = resolveListen("", "---\ndelay: 120s\non_team_reply: skip\n---\nYelan reviews Franky.", agentchannels.ListenThread{})
	if st.Delay != 120*time.Second || st.OnTeamReply != "skip" || st.Scope != "thread" {
		t.Fatalf("agent keys not applied: %+v", st)
	}
	if !strings.Contains(st.Instructions, "Answer only when") || !strings.HasSuffix(st.Instructions, "Yelan reviews Franky.") {
		t.Fatalf("agent body must follow the default: %q", st.Instructions)
	}
	st = resolveListen("", "---\nreplace_default: true\n---\nOnly mine.", agentchannels.ListenThread{})
	if st.Instructions != "Only mine." {
		t.Fatalf("replace_default: %q", st.Instructions)
	}
	zero := 0
	st = resolveListen("", "---\ndelay: 90s\n---\n", agentchannels.ListenThread{Prompt: "only WABA", DelaySec: &zero})
	if st.Delay != 0 || !strings.HasSuffix(st.Instructions, "For this thread: only WABA") {
		t.Fatalf("thread override: %+v", st)
	}
	st = resolveListen("", "---\ndelay: 5h\n---\n", agentchannels.ListenThread{})
	if st.Delay != listenMaxDelay {
		t.Fatalf("delay must be capped, got %s", st.Delay)
	}
}

func TestIsStopCommand(t *testing.T) {
	for _, s := range []string{"stop", "Stop!", "<@U123> stop", "berhenti.", "  STOP listening "} {
		if !isStopCommand(s) {
			t.Errorf("%q should stop", s)
		}
	}
	for _, s := range []string{"jangan stop dulu", "stop the server please", "bisa stop deploy?", ""} {
		if isStopCommand(s) {
			t.Errorf("%q must not stop", s)
		}
	}
}

// listenChannel builds a Channel whose held replies are recorded, not run.
func listenChannel(t *testing.T, agentDoc string) (*Channel, *fakeListenSessions, chan [3]any) {
	t.Helper()
	fake := newFakeListen(agentDoc)
	ch := New(agentconfig.SlackChannelConfig{})
	ch.sessions = fake
	got := make(chan [3]any, 4)
	ch.listenDispatchHook = func(sessionID string, ev *slackevents.MessageEvent, waited time.Duration, others bool) {
		got <- [3]any{ev.Text, waited, others}
	}
	return ch, fake, got
}

func listenMsg(user, text string) *slackevents.MessageEvent {
	return &slackevents.MessageEvent{User: user, Text: text, Channel: "C1", ThreadTimeStamp: "100.1", TimeStamp: "100.2"}
}

// The asker adding to the question restarts the wait and the latest
// message goes through once.
func TestListenWaitAskerAddsMore(t *testing.T) {
	ch, _, got := listenChannel(t, "---\ndelay: 1\n---\n")
	ch.queueListenMessage(t.Context(), "S", listenMsg("UCLIENT", "halo"), nil)
	time.Sleep(600 * time.Millisecond)
	ch.queueListenMessage(t.Context(), "S", listenMsg("UCLIENT", "mau tanya soal template"), []slackgo.File{})
	select {
	case r := <-got:
		if r[0] != "mau tanya soal template" || r[2] != false {
			t.Fatalf("dispatched %v", r)
		}
		if r[1].(time.Duration) < 1500*time.Millisecond {
			t.Fatalf("wait did not restart: waited %v", r[1])
		}
	case <-time.After(4 * time.Second):
		t.Fatal("held reply never went through")
	}
}

// Someone else answering during the wait: review passes it on marked,
// skip drops it without a turn.
func TestListenWaitOthersReplied(t *testing.T) {
	ch, _, got := listenChannel(t, "---\ndelay: 1\n---\n")
	ch.queueListenMessage(t.Context(), "S", listenMsg("UCLIENT", "error 131026 kenapa?"), nil)
	ch.queueListenMessage(t.Context(), "S", listenMsg("UTEAM", "sudah dicek ya"), nil)
	select {
	case r := <-got:
		if r[0] != "error 131026 kenapa?" || r[2] != true {
			t.Fatalf("review: dispatched %v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("review: held reply never went through")
	}

	ch, _, got = listenChannel(t, "---\ndelay: 1\non_team_reply: skip\n---\n")
	ch.queueListenMessage(t.Context(), "S", listenMsg("UCLIENT", "error 131026 kenapa?"), nil)
	ch.noteListenOther("S", "BOTHER")
	select {
	case r := <-got:
		t.Fatalf("skip must not dispatch, got %v", r)
	case <-time.After(1500 * time.Millisecond):
	}
}

// Stopping drops a held reply and records the stop, which beats a scope.
func TestListenOffCancelsWait(t *testing.T) {
	ch, fake, got := listenChannel(t, "---\ndelay: 1\n---\n")
	fake.SetAutoReply("S", true)
	ch.queueListenMessage(t.Context(), "S", listenMsg("UCLIENT", "halo"), nil)
	ch.ListenOff("S", "C1", "100.1", "test", false)
	if fake.AutoReplyOn("S") || !fake.ListenThread("S").Stopped {
		t.Fatalf("off not recorded: auto=%v thread=%+v", fake.AutoReplyOn("S"), fake.ListenThread("S"))
	}
	select {
	case r := <-got:
		t.Fatalf("stopped thread dispatched %v", r)
	case <-time.After(1500 * time.Millisecond):
	}
}

// The block of a listening turn reaches past the waking message, without it.
func TestCatchUpEntriesUntil(t *testing.T) {
	ch := New(agentconfig.SlackChannelConfig{})
	msgs := []slackgo.Message{
		{Msg: slackgo.Msg{Timestamp: "100.2", User: "UCLIENT", Text: "question"}},
		{Msg: slackgo.Msg{Timestamp: "100.3", User: "UTEAM", Text: "answered"}},
		{Msg: slackgo.Msg{Timestamp: "100.4", BotID: "B2", Username: "otherbot", SubType: "bot_message", Text: "bot answer"}},
	}
	got := ch.catchUpEntriesUntil(msgs, "100.2", "100.1", "200.0")
	if len(got) != 2 || got[0].Text != "answered" || got[1].Text != "bot answer" {
		t.Fatalf("entries = %+v", got)
	}
	if old := ch.catchUpEntries(msgs, "100.3", "100.1"); len(old) != 1 || old[0].Text != "question" {
		t.Fatalf("plain catch-up changed: %+v", old)
	}
}

func TestListenNote(t *testing.T) {
	n := listenNote(listenTurn{st: listenSettings{Instructions: "Only WABA."}, waited: 61 * time.Second, othersReplied: true})
	for _, want := range []string{"[listen mode]", "held it 1m1s", "Someone else", "[silent]", "Only WABA."} {
		if !strings.Contains(n, want) {
			t.Errorf("note lacks %q: %s", want, n)
		}
	}
	if !strings.HasPrefix(n, "[listen mode]") {
		t.Fatalf("note must open with the marker the [silent] rule names: %s", n)
	}
}
