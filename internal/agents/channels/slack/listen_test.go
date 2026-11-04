package slack

import (
	"fmt"
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
	if st.Delay != 15*time.Second || st.Scope != "thread" || st.OnTeamReply != "review" || !strings.Contains(st.Instructions, "[silent]") {
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

// Every reply a listening thread hears lands in the session at once, and
// someone else joining in restarts the wait like the asker does.
func TestListenWaitHeardAndRestartedByAnyone(t *testing.T) {
	ch, _, got := listenChannel(t, "---\ndelay: 1\n---\n")
	var mu sync.Mutex
	var heard []map[string]string
	ch.SetHeardFn(func(sessionID, text string, extras map[string]string) {
		mu.Lock()
		defer mu.Unlock()
		e := map[string]string{"text": text}
		for k, v := range extras {
			e[k] = v
		}
		heard = append(heard, e)
	})
	// Names known up front: a users.info lookup would reach the network.
	ch.heardNames = map[string]string{"UASKER": "Asker Example", "UOTHER": "Other Example"}
	first := listenMsg("UASKER", "kenapa form-nya kosong?")
	ch.queueListenMessage(t.Context(), "S", first, nil)
	time.Sleep(600 * time.Millisecond)
	other := listenMsg("UOTHER", "aku juga lihat begitu")
	other.TimeStamp = "100.3"
	ch.queueListenMessage(t.Context(), "S", other, []slackgo.File{{Name: "shot.png"}})
	select {
	case r := <-got:
		if r[0] != "kenapa form-nya kosong?" || r[2] != true {
			t.Fatalf("dispatched %v", r)
		}
		if r[1].(time.Duration) < 1500*time.Millisecond {
			t.Fatalf("another reply did not restart the wait: waited %v", r[1])
		}
	case <-time.After(4 * time.Second):
		t.Fatal("held reply never went through")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(heard) != 2 {
		t.Fatalf("heard %d replies, want 2: %v", len(heard), heard)
	}
	if heard[0]["text"] != "kenapa form-nya kosong?" || heard[0]["msg_ts"] != "100.2" || heard[0]["user"] != "UASKER" {
		t.Fatalf("first heard = %v", heard[0])
	}
	if heard[0]["name"] != "Asker Example" {
		t.Fatalf("first heard name = %q", heard[0]["name"])
	}
	if heard[1]["user"] != "UOTHER" || heard[1]["files"] != "shot.png" || heard[1]["hold_id"] != heard[0]["hold_id"] || heard[0]["hold_id"] == "" {
		t.Fatalf("second heard = %v", heard[1])
	}
}

// A reply while the agent works in the thread goes to it at once, not
// after the turn and a quiet thread.
func TestListenReplyJoinsRunningTurn(t *testing.T) {
	ch, _, got := listenChannel(t, "---\ndelay: 5\n---\n")
	ch.turns["S"] = &turn{channelID: "C1", threadTS: "100.1", running: true}
	ch.queueListenMessage(t.Context(), "S", listenMsg("UASKER", "sekalian yang EN ya"), nil)
	select {
	case r := <-got:
		if r[0] != "sekalian yang EN ya" || r[1].(time.Duration) != 0 {
			t.Fatalf("dispatched %v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reply waited although a turn was running")
	}
	ch.mu.Lock()
	held := ch.listenWaits["S"]
	m := ch.working["S"]
	ch.mu.Unlock()
	if held != nil || len(m.ts) != 1 || m.ts[0] != "100.2" {
		t.Fatalf("held=%v working=%+v", held, m)
	}
}

// Another bot's reply is heard under the bot's own name, marked as a bot.
func TestHeardBotReplyNamedAndMarked(t *testing.T) {
	ch := New(agentconfig.SlackChannelConfig{})
	var got map[string]string
	ch.SetHeardFn(func(_, text string, extras map[string]string) {
		got = map[string]string{"text": text}
		for k, v := range extras {
			got[k] = v
		}
	})
	ev := &slackevents.MessageEvent{BotID: "B01TEST", Username: "intake-bot", Text: "form baru masuk", Channel: "C1", ThreadTimeStamp: "100.1", TimeStamp: "100.4"}
	ch.reportHeard("S", nil, ev, nil)
	if got["name"] != "intake-bot" || got["bot"] != "1" || got["user"] != "B01TEST" || got["hold_id"] != "" || got["text"] != "form baru masuk" {
		t.Fatalf("heard = %v", got)
	}
}

// A bot's reply goes through listen like a person's; after listenBotStreak
// bot-only turns in a row it is heard but starts nothing until a person
// speaks.
func TestListenBotRepliesCapped(t *testing.T) {
	ch, _, got := listenChannel(t, "---\ndelay: 1\n---\n")
	heard := 0
	ch.SetHeardFn(func(string, string, map[string]string) { heard++ })
	bot := func(ts string) *slackevents.MessageEvent {
		return &slackevents.MessageEvent{BotID: "B01TEST", Username: "other-bot", Text: "status ok", Channel: "C1", ThreadTimeStamp: "100.1", TimeStamp: ts}
	}
	for i := 0; i < listenBotStreak; i++ {
		ch.queueListenMessage(t.Context(), "S", bot(fmt.Sprintf("100.%d", 2+i)), nil)
		select {
		case <-got:
		case <-time.After(3 * time.Second):
			t.Fatalf("bot reply %d never went to the agent", i+1)
		}
	}
	ch.queueListenMessage(t.Context(), "S", bot("100.9"), nil)
	select {
	case r := <-got:
		t.Fatalf("bot-only turn past the limit: %v", r)
	case <-time.After(1500 * time.Millisecond):
	}
	if heard != listenBotStreak+1 {
		t.Fatalf("heard %d bot replies, want %d", heard, listenBotStreak+1)
	}
	ch.heardNames = map[string]string{"UASKER": "Asker Example"}
	ch.queueListenMessage(t.Context(), "S", listenMsg("UASKER", "lanjut"), nil)
	if n := ch.botTurnsInARow("S"); n != 0 {
		t.Fatalf("a person speaking left the bot count at %d", n)
	}
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("a person's reply did not go through")
	}
}

// A heard reply is shown without Slack's markup: names, links, channels.
func TestHeardTextDropsSlackMarkup(t *testing.T) {
	ch := New(agentconfig.SlackChannelConfig{})
	ch.heardNames = map[string]string{"U01TEST": "Dev Example"}
	got := ch.heardText("mbk <@U01TEST> cek <https://example.com/a?b=1|example.com/a…> di <#C01TESTCHAN|general> <!here> &lt;ok&gt; <https://example.com/x> <https://example.com/doc|the doc> <@U02NONAME>")
	want := "mbk @Dev Example cek https://example.com/a?b=1 di #general @here <ok> https://example.com/x the doc (https://example.com/doc) @U02NONAME"
	if got != want {
		t.Fatalf("heardText =\n%q\nwant\n%q", got, want)
	}
}

// The thread carries one 👀, on the latest reply heard, and counts them all.
func TestListenWaitOneMarkerOnLatest(t *testing.T) {
	ch, _, _ := listenChannel(t, "---\ndelay: 5\n---\n")
	ch.queueListenMessage(t.Context(), "S", listenMsg("UASKER", "satu"), nil)
	second := listenMsg("UASKER", "dua")
	second.TimeStamp = "100.3"
	ch.queueListenMessage(t.Context(), "S", second, nil)
	w := ch.takeListenWait("S")
	if w == nil || w.eyesTS != "100.3" || w.heard != 2 {
		t.Fatalf("wait = %+v", w)
	}
}

// ⏳ sits on the latest reply a turn works on until the turn ends.
func TestWorkingMarkClearedAtTurnEnd(t *testing.T) {
	ch := New(agentconfig.SlackChannelConfig{})
	ch.markWorking("S", "C1", []string{"100.2"})
	ch.markWorking("S", "C1", []string{"100.3"})
	ch.mu.Lock()
	m := ch.working["S"]
	ch.mu.Unlock()
	if m.channel != "C1" || len(m.ts) != 1 || m.ts[0] != "100.3" {
		t.Fatalf("working = %+v", m)
	}
	ch.clearWorking("S")
	ch.mu.Lock()
	_, left := ch.working["S"]
	ch.mu.Unlock()
	if left {
		t.Fatal("working mark survived the turn")
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
	n := listenNote(listenTurn{waited: 61 * time.Second, othersReplied: true, threadPrompt: "only WABA"})
	if !strings.HasPrefix(n, "[listen mode · held 1m1s · someone else replied meanwhile]") || !strings.Contains(n, "For this thread: only WABA") {
		t.Fatalf("note = %s", n)
	}
	if strings.Contains(n, "LISTEN.md") {
		t.Fatalf("unchanged docs must stay in the system prompt: %s", n)
	}
	n = listenNote(listenTurn{docsChanged: true, docInstructions: "New rule."})
	if !strings.HasSuffix(n, "New rule.") {
		t.Fatalf("changed docs must ride along: %s", n)
	}
	if strings.Contains(n, "mention from") {
		t.Fatalf("no pending mention, no mention line: %s", n)
	}
	// A reply that joins a turn still answering a mention says so, or the
	// agent reads the whole turn as one that only needs [silent].
	n = listenNote(listenTurn{pendingMention: "Alice"})
	if !strings.Contains(n, "mention from Alice you have not answered yet") {
		t.Fatalf("pending mention missing: %s", n)
	}
}

// The system prompt carries the rules and LISTEN.md; an edit afterwards
// is noticed once.
func TestListenPromptAndDocChange(t *testing.T) {
	ch, fake, _ := listenChannel(t, "Franky rules.")
	p := ch.ListenPrompt("S")
	for _, want := range []string{"## Slack listen mode", "[react :pray:]", "Franky rules."} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q: %s", want, p)
		}
	}
	instr := resolveListen("", "Franky rules.", agentchannels.ListenThread{}).Instructions
	if ch.listenDocsChanged("S", instr) {
		t.Fatal("unchanged docs reported as changed")
	}
	fake.agentDoc = "Franky rules v2."
	if !ch.listenDocsChanged("S", resolveListen("", fake.agentDoc, agentchannels.ListenThread{}).Instructions) {
		t.Fatal("edited docs not noticed")
	}
	if ch.listenDocsChanged("NEW", instr) {
		t.Fatal("a session with no process yet gets the docs from its spawn")
	}
}

func TestEffectiveListen(t *testing.T) {
	got := EffectiveListen("", "---\ndelay: 2m\n---\nYelan rules.")
	if got.DelaySec != 120 || got.Scope != "thread" || got.OnTeamReply != "review" {
		t.Fatalf("effective = %+v", got)
	}
	if !strings.Contains(got.Instructions, "inquiry/main thread") || !strings.HasSuffix(got.Instructions, "Yelan rules.") {
		t.Fatalf("instructions = %q", got.Instructions)
	}
}
