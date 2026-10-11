package slackremote

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/remote"
)

// fakeSlack is a Slack Web API double: it records posts and serves the
// replies the test scripts.
type fakeSlack struct {
	mu      sync.Mutex
	n       int
	posts   []map[string]string
	replies []map[string]any // returned by conversations.replies
	fetches int
	token   string
	// missing is a thread ts conversations.replies answers thread_not_found for.
	missing string
	// reads records each read's "oldest..latest"; filter makes a read
	// return only the messages between them, a page at most, as Slack
	// does.
	reads  []string
	filter bool
	// failOld makes a read bounded by latest (the older window) fail.
	failOld bool
}

func (f *fakeSlack) nextTS() string {
	f.n++
	return fmt.Sprintf("1700000000.%06d", f.n)
}

func (f *fakeSlack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
		return
	}
	out := map[string]any{"ok": true}
	switch strings.TrimPrefix(r.URL.Path, "/") {
	case "auth.test":
		out["user_id"], out["bot_id"] = "UWICK", "BWICK"
	case "conversations.open":
		out["channel"] = map[string]any{"id": "D1"}
	case "chat.postMessage":
		ts := f.nextTS()
		f.posts = append(f.posts, map[string]string{"channel": r.Form.Get("channel"), "text": r.Form.Get("text"), "thread_ts": r.Form.Get("thread_ts"), "ts": ts})
		out["ts"] = ts
	case "conversations.replies", "conversations.history":
		if f.missing != "" && r.Form.Get("ts") == f.missing {
			out = map[string]any{"ok": false, "error": "thread_not_found"}
			break
		}
		f.fetches++
		oldest, latest := r.Form.Get("oldest"), r.Form.Get("latest")
		f.reads = append(f.reads, oldest+".."+latest)
		if f.failOld && latest != "" {
			out = map[string]any{"ok": false, "error": "ratelimited"}
			break
		}
		msgs := f.replies
		if f.filter {
			msgs = nil
			for _, m := range f.replies {
				ts := m["ts"].(string)
				if (oldest == "" || tsLess(oldest, ts)) && (latest == "" || tsLess(ts, latest)) && len(msgs) < repliesLimit {
					msgs = append(msgs, m)
				}
			}
		}
		out["messages"] = msgs
	default:
		out = map[string]any{"ok": false, "error": "unknown_method"}
	}
	_ = json.NewEncoder(w).Encode(out)
}

// reply scripts a message from user, ts after every post so far.
func (f *fakeSlack) reply(user, text string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ts := f.nextTS()
	thread := ""
	if len(f.posts) > 0 {
		thread = f.posts[0]["ts"]
	}
	f.replies = append(f.replies, map[string]any{"ts": ts, "thread_ts": thread, "user": user, "text": text})
	return ts
}

func (f *fakeSlack) lastToken(t *testing.T) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := regexp.MustCompile(`END RESPONSE ([0-9a-f]+)$`).FindStringSubmatch(f.posts[len(f.posts)-1]["text"])
	if m == nil {
		t.Fatalf("no marker in %q", f.posts[len(f.posts)-1]["text"])
	}
	return m[1]
}

func setup(t *testing.T, cfg Config) (*fakeSlack, *Source, *Router) {
	t.Helper()
	f := &fakeSlack{token: "xoxb-test"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old })
	oldSteps := remote.PullSteps
	remote.PullSteps = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}
	t.Cleanup(func() { remote.PullSteps = oldSteps })
	rt := NewRouter()
	src := NewSource(cfg, Deps{API: HTTPAPI{Token: f.token}, Router: rt, WickIDs: func() []string { return []string{"UOTHERWICK"} }})
	return f, src, rt
}

type result struct {
	text string
	line map[string]any
}

// start spawns src with one message; the returned func waits for the
// turn's text and closing line.
func start(t *testing.T, src *Source, msg string) func() result {
	t.Helper()
	p, err := remote.Spawner{Source: src}.Spawn(context.Background(), provider.SpawnOptions{InitialMessage: msg, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// Wait lets the turn's End finish writing state before TempDir goes.
	t.Cleanup(func() { _ = p.Kill(); _ = p.Wait() })
	ch := make(chan result, 1)
	go func() {
		sc := bufio.NewScanner(p.Stdout())
		var sb strings.Builder
		for sc.Scan() {
			var l map[string]any
			_ = json.Unmarshal(sc.Bytes(), &l)
			switch l["type"] {
			case "assistant":
				for _, c := range l["message"].(map[string]any)["content"].([]any) {
					sb.WriteString(c.(map[string]any)["text"].(string))
				}
			case "result":
				ch <- result{sb.String(), l}
				return
			}
		}
	}()
	return func() result {
		select {
		case r := <-ch:
			return r
		case <-time.After(5 * time.Second):
			t.Fatal("turn did not end")
			return result{}
		}
	}
}

func waitPost(t *testing.T, f *fakeSlack) {
	t.Helper()
	for i := 0; i < 200; i++ {
		f.mu.Lock()
		n := len(f.posts)
		f.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("nothing posted")
}

func TestMarkerEndsTurnAndIsStripped(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetDM, User: "UBOT", GraceSec: -1})
	wait := start(t, src, "hello")
	waitPost(t, f)
	f.mu.Lock()
	post := f.posts[0]
	f.mu.Unlock()
	if post["channel"] != "D1" || !strings.HasPrefix(post["text"], "<@UBOT> hello\n\nWhen your reply is complete") {
		t.Fatalf("post = %+v", post)
	}
	tok := f.lastToken(t)
	f.reply("UBOT", "Hi there.\nEND RESPONSE "+tok)
	r := wait()
	if r.text != "Hi there." || r.line["result"] != "Hi there." || r.line["is_error"] != false {
		t.Fatalf("result = %+v", r)
	}
	if strings.Contains(r.text, "END RESPONSE") {
		t.Fatal("marker leaked")
	}
	// Polling stops once the turn is done.
	f.mu.Lock()
	n := f.fetches
	f.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fetches != n {
		t.Fatalf("polled after done: %d → %d", n, f.fetches)
	}
}

func TestIdleEndsWithoutMarker(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetDM, User: "UBOT"})
	src.cfg.IdleSec = 1
	wait := start(t, src, "hello")
	waitPost(t, f)
	f.reply("UBOT", "I forgot the marker")
	r := wait()
	if r.text != "I forgot the marker" || r.line["remote_note"] != remote.NoteNoMarker {
		t.Fatalf("result = %+v", r)
	}
}

func TestMultiMessageAndLoopGuard(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", MentionID: "UBOT"})
	wait := start(t, src, "q")
	waitPost(t, f)
	f.mu.Lock()
	post := f.posts[0]
	f.mu.Unlock()
	if !strings.HasPrefix(post["text"], "<@UBOT> q") {
		t.Fatalf("mention missing: %q", post["text"])
	}
	tok := f.lastToken(t)
	f.reply("UWICK", "wick's own echo")
	f.reply("UOTHERWICK", "another wick agent")
	f.reply("USTRANGER", "not the target")
	f.reply("UBOT", "part one")
	f.reply("UBOT", "part two\nEND RESPONSE "+tok)
	r := wait()
	if r.text != "part one\n\npart two" {
		t.Fatalf("text = %q", r.text)
	}
}

func TestPushEditsStream(t *testing.T) {
	f, src, rt := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", GraceSec: -1})
	remote.PullSteps = []time.Duration{time.Hour} // events only
	wait := start(t, src, "q")
	waitPost(t, f)
	for rt.Waiting() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	tok := f.lastToken(t)
	f.mu.Lock()
	thread := f.posts[0]["ts"]
	f.mu.Unlock()
	rt.Dispatch(Message{Channel: "C1", TS: "1700000001.000001", ThreadTS: thread, User: "UBOT", Text: "Thinking…"})
	rt.Dispatch(Message{Channel: "C1", TS: "1700000001.000001", ThreadTS: thread, User: "UBOT", Text: "Hello", Edited: true})
	rt.Dispatch(Message{Channel: "C1", TS: "1700000001.000001", ThreadTS: thread, User: "UBOT", Text: "Hello world", Edited: true})
	rt.Dispatch(Message{Channel: "C1", TS: "1700000001.000001", ThreadTS: thread, User: "UBOT", Text: "Hello world\nEND RESPONSE " + tok, Edited: true})
	r := wait()
	if r.text != "Hello world" || r.line["result"] != "Hello world" {
		t.Fatalf("result = %+v", r)
	}
	for i := 0; rt.Waiting() != 0; i++ {
		if i > 200 {
			t.Fatal("turn still routed after it ended")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNextTurnRepliesInThread(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", Marker: new(bool)})
	dir := t.TempDir()
	if _, err := src.Send(context.Background(), remote.Turn{Text: "one", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	src.End(remote.Handle{})
	if _, err := src.Send(context.Background(), remote.Turn{Text: "two", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	src.End(remote.Handle{})
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.posts[0]["thread_ts"] != "" || f.posts[1]["thread_ts"] != f.posts[0]["ts"] {
		t.Fatalf("posts = %+v", f.posts)
	}
	if strings.Contains(f.posts[1]["text"], "END RESPONSE") {
		t.Fatal("marker off but instruction sent")
	}
}

func TestAccountOwner(t *testing.T) {
	if err := CheckAccountOwner("u1", "u1"); err != nil {
		t.Fatal(err)
	}
	if err := CheckAccountOwner("u2", "u1"); err != ErrNotOwnAccount {
		t.Fatalf("someone else's account allowed: %v", err)
	}
	c := Config{ConnectorID: "c", Identity: IdentityUser, Target: TargetDM, User: "U"}
	if err := c.Normalize(); err == nil {
		t.Fatal("identity user without account accepted")
	}
}

func TestProgressLine(t *testing.T) {
	for in, want := range map[string]bool{"Thinking…": true, "_Bash: ls -la_": true, ":hourglass: Working": true, "Bash is a shell": false, "Hello": false, "_checking the repo list…_": true, "Looking into it...": true, "Here is the answer.": false} {
		if _, ok := progressLine(in); ok != want {
			t.Errorf("progressLine(%q) = %v", in, ok)
		}
	}
}

func TestFromEventAndRouterKeys(t *testing.T) {
	if _, ok := FromEvent("C1", "channel_join", "1.1", "", "U", "", "joined", nil); ok {
		t.Fatal("channel_join routed")
	}
	m, ok := FromEvent("C1", "message_changed", "9.9", "", "", "", "", &Message{TS: "1.2", ThreadTS: "1.0", User: "UBOT", Text: "edited"})
	if !ok || m.Channel != "C1" || m.TS != "1.2" || !m.Edited || m.Text != "edited" {
		t.Fatalf("message_changed = %+v %v", m, ok)
	}
	rt := NewRouter()
	tr := newTracker("C1", "1.0", "1.0", "", false, map[string]bool{}, func(Message) bool { return true })
	rt.add(tr)
	rt.Dispatch(Message{Channel: "C2", TS: "1.5", ThreadTS: "1.0", User: "UBOT", Text: "other channel"})
	rt.Dispatch(Message{Channel: "C1", TS: "1.6", ThreadTS: "1.3", User: "UBOT", Text: "other thread"})
	if len(tr.events) != 0 {
		t.Fatalf("foreign messages routed: %d", len(tr.events))
	}
	rt.Dispatch(m)
	if ev := <-tr.events; ev.Kind != remote.EventText || ev.Text != "edited" {
		t.Fatalf("event = %+v", ev)
	}
	d, ok := FromEvent("C1", "message_deleted", "9.9", "", "", "", "", &Message{TS: "1.2", ThreadTS: "1.0", User: "UBOT"})
	if !ok || d.TS != "1.2" || !d.Deleted {
		t.Fatalf("message_deleted = %+v %v", d, ok)
	}
	rt.remove(tr)
	if rt.Waiting() != 0 {
		t.Fatal("still waiting")
	}
}

func TestMentionTargetEveryTurn(t *testing.T) {
	off := false
	for name, tc := range map[string]struct {
		cfg   Config
		want1 string
		want2 string
	}{
		// Unset = on, as for agents saved before the setting existed.
		"dm default on": {Config{ConnectorID: "c", Target: TargetDM, User: "UBOT", Marker: &off}, "<@UBOT> q1", "<@UBOT> q2"},
		"channel on":    {Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", MentionID: "UBOT", Marker: &off}, "<@UBOT> q1", "<@UBOT> q2"},
		"channel off":   {Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", MentionID: "UBOT", Marker: &off, MentionTarget: &off}, "q1", "q2"},
		"no target id":  {Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", Marker: &off}, "q1", "q2"},
		"already there": {Config{ConnectorID: "c", Target: TargetDM, User: "UBOT", Marker: &off}, "<@UBOT> q1", "<@UBOT> q2"},
	} {
		t.Run(name, func(t *testing.T) {
			f, src, _ := setup(t, tc.cfg)
			dir := t.TempDir()
			first, second := "q1", "q2"
			if name == "already there" {
				first, second = "<@UBOT> q1", "<@UBOT> q2"
			}
			if _, err := src.Send(context.Background(), remote.Turn{Text: first, SessionDir: dir}); err != nil {
				t.Fatal(err)
			}
			// The follow-up goes to the thread the first turn opened.
			if _, err := src.Send(context.Background(), remote.Turn{Text: second, SessionDir: dir}); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.posts) != 2 {
				t.Fatalf("posts = %d", len(f.posts))
			}
			if f.posts[0]["text"] != tc.want1 || f.posts[1]["text"] != tc.want2 {
				t.Fatalf("texts = %q, %q", f.posts[0]["text"], f.posts[1]["text"])
			}
			if f.posts[1]["thread_ts"] == "" {
				t.Fatal("follow-up not in the thread")
			}
		})
	}
}

func TestMentionTargetJSONDefault(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{"connector_id":"c","target":"dm","user":"U1"}`), &c); err != nil {
		t.Fatal(err)
	}
	if !c.MentionOn() {
		t.Fatal("an agent saved before mention_target existed must default to on")
	}
	if err := json.Unmarshal([]byte(`{"mention_target":false}`), &c); err != nil || c.MentionOn() {
		t.Fatalf("explicit off not kept: %v", err)
	}
}

func TestTimeoutHint(t *testing.T) {
	off := false
	if h := NewSource(Config{Target: TargetDM, User: "U1"}, Deps{}).TimeoutHint(); h != "" {
		t.Fatalf("mention on with a target: hint %q", h)
	}
	if h := NewSource(Config{Target: TargetDM, User: "U1", MentionTarget: &off}, Deps{}).TimeoutHint(); !strings.Contains(h, "Always @mention the target") {
		t.Fatalf("mention off: hint %q", h)
	}
	if h := NewSource(Config{Target: TargetChannel, Channel: "C1"}, Deps{}).TimeoutHint(); !strings.Contains(h, "bot's ID") {
		t.Fatalf("no target id: hint %q", h)
	}
}

// A thread opened for another channel is not continued after the agent is
// pointed elsewhere: Slack would post top-level in the new channel while
// the turn listened to the old thread, missing every reply.
func TestNewTargetStartsANewThread(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", Marker: new(bool)})
	dir := t.TempDir()
	if _, err := src.Send(context.Background(), remote.Turn{Text: "one", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	src.End(remote.Handle{})
	src.cfg.Channel = "C2"
	if _, err := src.Send(context.Background(), remote.Turn{Text: "two", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	src.End(remote.Handle{})
	if _, err := src.Send(context.Background(), remote.Turn{Text: "three", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	src.End(remote.Handle{})
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.posts[1]["channel"] != "C2" || f.posts[1]["thread_ts"] != "" {
		t.Fatalf("turn after the switch reused the old thread: %+v", f.posts[1])
	}
	if f.posts[2]["thread_ts"] != f.posts[1]["ts"] {
		t.Fatalf("next turn not in the new thread: %+v", f.posts[2])
	}
}

// State written before Target was kept cannot say which channel its thread
// is in, so the next turn opens a new thread rather than guess.
func TestLegacyStateStartsANewThread(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", Marker: new(bool)})
	dir := t.TempDir()
	saveState(dir, State{Channel: "C1", ThreadTS: "1600000000.000001"})
	if _, err := src.Send(context.Background(), remote.Turn{Text: "one", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	src.End(remote.Handle{})
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.posts[0]["thread_ts"] != "" {
		t.Fatalf("legacy thread reused: %+v", f.posts[0])
	}
	if st := LoadState(dir); st.Target != "channel:C1" || st.ThreadTS != f.posts[0]["ts"] {
		t.Fatalf("state = %+v", st)
	}
}

// editedEarlierTurn plays the thread where the remote answers turn B by
// editing the message it posted during turn A: turn A gets first (the
// remote's message), turn B is sent, then that message is edited to
// then — with edited.ts after turn B's message, or without edited when
// noEdited. It returns what turn B's read reported and the Recheck after
// it.
func editedEarlierTurn(t *testing.T, first func(tokA string) string, then func(tokA, tokB string) string, noEdited ...bool) ([]remote.Event, Recheck) {
	t.Helper()
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1"})
	f.filter = true
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := src.Send(ctx, remote.Turn{Text: "question A", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	tokA := f.lastToken(t)
	msgTS := f.reply("UBOT", first(tokA))
	if _, _, err := src.Fetch(ctx, remote.Handle{}); err != nil {
		t.Fatal(err)
	}
	src.End(remote.Handle{})
	if _, err := src.Send(ctx, remote.Turn{Text: "question B", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	tokB := f.lastToken(t)
	f.mu.Lock()
	sentA, sentB := f.posts[0]["ts"], f.posts[1]["ts"]
	for _, m := range f.replies {
		if m["ts"] == msgTS {
			m["text"] = then(tokA, tokB)
			if len(noEdited) == 0 || !noEdited[0] {
				m["edited"] = map[string]any{"user": "UBOT", "ts": f.nextTS()}
			}
		}
	}
	f.reads = nil
	f.mu.Unlock()
	evs, _, err := src.Fetch(ctx, remote.Handle{})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	reads := strings.Join(f.reads, " ")
	f.mu.Unlock()
	if want := sentB + ".. " + sentA + ".." + sentB; reads != want {
		t.Fatalf("turn B read %q, want %q", reads, want)
	}
	src.End(remote.Handle{})
	rc, err := src.Recheck(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	return evs, rc
}

func doneText(evs []remote.Event) (string, bool) {
	for _, ev := range evs {
		if ev.Kind == remote.EventDone {
			return ev.Text, true
		}
	}
	return "", false
}

func TestOlderMessageEditedWithThisTurnsMarker(t *testing.T) {
	evs, rc := editedEarlierTurn(t,
		func(string) string { return "_Lagi mengurai…_\n_Tinggal satu cek lagi…_" },
		func(_, tokB string) string { return "The full answer.\n\nEND RESPONSE " + tokB },
	)
	if got, ok := doneText(evs); !ok || got != "The full answer." {
		t.Fatalf("turn B done = %q %v, events %+v", got, ok, evs)
	}
	if !rc.Done || rc.Text != "The full answer." {
		t.Fatalf("recheck = %+v", rc)
	}
}

func TestEarlierMessageNotTakenWithoutThisTurnsMarker(t *testing.T) {
	cases := map[string]struct {
		first func(tokA string) string
		then  func(tokA, tokB string) string
	}{
		// turn A ended on progress; the remote finishes A late, unmarked
		"unfinished turn finished late": {
			func(string) string { return "_Lagi mengurai…_" },
			func(string, string) string { return "Answer A, finished late" },
		},
		// turn A ended on its marker; an edit keeps that marker
		"marked answer unfurled": {
			func(tokA string) string { return "Answer A https://x.example\nEND RESPONSE " + tokA },
			func(tokA, _ string) string { return "Answer A https://x.example\nEND RESPONSE " + tokA },
		},
		"marked answer reworded": {
			func(tokA string) string { return "Answer A\nEND RESPONSE " + tokA },
			func(tokA, _ string) string { return "Answer A, reworded\nEND RESPONSE " + tokA },
		},
		// turn A ended on idle with a reply, edited later
		"unmarked answer edited": {
			func(string) string { return "Answer A" },
			func(string, string) string { return "Answer A, edited" },
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			evs, rc := editedEarlierTurn(t, c.first, c.then)
			if txt := lastText(evs); txt != "" {
				t.Fatalf("turn B took %q", txt)
			}
			if _, ok := doneText(evs); ok || rc.Done || rc.Text != "" {
				t.Fatalf("events %+v, recheck %+v", evs, rc)
			}
		})
	}
}

// sendTurn sends one turn through src and returns its handle and token.
func sendTurn(t *testing.T, f *fakeSlack, src *Source, dir, text string) (remote.Handle, string) {
	t.Helper()
	h, err := src.Send(context.Background(), remote.Turn{Text: text, SessionDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return h, f.lastToken(t)
}

// changed is the message_changed event of the remote's message ts, as
// the Slack channel hands it on.
func changed(thread, ts, editedTS, text string) Message {
	m, _ := FromEvent("C1", "message_changed", editedTS, "", "", "", "",
		&Message{TS: ts, ThreadTS: thread, User: "UBOT", EditedTS: editedTS, Text: text})
	return m
}

func drain(ch <-chan remote.Event) (evs []remote.Event) {
	for {
		select {
		case ev := <-ch:
			evs = append(evs, ev)
		default:
			return evs
		}
	}
}

// TestRealSequenceThroughEvents: the reported thread, through Send/End and
// the router — progress note during turn A, turn B sent, the note edited
// into B's answer with B's marker.
func TestRealSequenceThroughEvents(t *testing.T) {
	f, src, rt := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1"})
	f.filter = true
	ctx := context.Background()
	dir := t.TempDir()
	sendTurn(t, f, src, dir, "question A")
	thread := f.posts[0]["ts"]
	note := f.reply("UBOT", "_Lagi mengurai…_\n_Tinggal satu cek lagi…_")
	rt.Dispatch(Message{Channel: "C1", TS: note, ThreadTS: thread, User: "UBOT", Text: "_Lagi mengurai…_"})
	src.End(remote.Handle{})
	h, tokB := sendTurn(t, f, src, dir, "question B")
	ch, err := src.Receive(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	edit := f.nextTS()
	final := "The full answer.\nEND RESPONSE " + tokB
	f.mu.Lock()
	for _, m := range f.replies {
		if m["ts"] == note {
			m["text"], m["edited"] = final, map[string]any{"ts": edit}
		}
	}
	f.mu.Unlock()
	rt.Dispatch(changed(thread, note, edit, final))
	if got, ok := doneText(drain(ch)); !ok || got != "The full answer." {
		t.Fatalf("turn B done = %q %v", got, ok)
	}
	if st := LoadState(dir); st.LastTS != f.posts[1]["ts"] {
		t.Fatalf("state lastTS = %q, want our message %q", st.LastTS, f.posts[1]["ts"])
	}
	src.End(remote.Handle{})
	if rc, err := src.Recheck(ctx, dir); err != nil || !rc.Done || rc.Text != "The full answer." {
		t.Fatalf("recheck = %+v %v", rc, err)
	}
}

// TestOlderThanTurnBeforeNotTaken: a message from turn A edited with turn
// C's marker is outside turn C's window (after B's message) — neither an
// event nor a read nor Recheck takes it.
func TestOlderThanTurnBeforeNotTaken(t *testing.T) {
	f, src, rt := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1"})
	f.filter = true
	ctx := context.Background()
	dir := t.TempDir()
	sendTurn(t, f, src, dir, "question A")
	thread := f.posts[0]["ts"]
	note := f.reply("UBOT", "_Lagi mengurai…_")
	src.End(remote.Handle{})
	sendTurn(t, f, src, dir, "question B")
	src.End(remote.Handle{})
	h, tokC := sendTurn(t, f, src, dir, "halo?")
	ch, _ := src.Receive(ctx, h)
	edit := f.nextTS()
	final := "Answer.\nEND RESPONSE " + tokC
	f.mu.Lock()
	for _, m := range f.replies {
		if m["ts"] == note {
			m["text"], m["edited"] = final, map[string]any{"ts": edit}
		}
	}
	f.mu.Unlock()
	rt.Dispatch(changed(thread, note, edit, final))
	evs := drain(ch)
	fetched, _, err := src.Fetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	evs = append(evs, fetched...)
	if lastText(evs) != "" {
		t.Fatalf("events = %+v", evs)
	}
	src.End(remote.Handle{})
	if rc, err := src.Recheck(ctx, dir); err != nil || rc.Done || rc.Text != "" {
		t.Fatalf("recheck = %+v %v", rc, err)
	}
}

// TestLongEarlierTurnKeepsNewestReplies: a turn before with more than a
// page of messages does not push this turn's reply off the read.
func TestLongEarlierTurnKeepsNewestReplies(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1"})
	f.filter = true
	ctx := context.Background()
	dir := t.TempDir()
	sendTurn(t, f, src, dir, "question A")
	for i := 0; i < repliesLimit+20; i++ {
		f.reply("UBOT", fmt.Sprintf("line %d", i))
	}
	src.End(remote.Handle{})
	h, tokB := sendTurn(t, f, src, dir, "question B")
	f.reply("UBOT", "Answer B\nEND RESPONSE "+tokB)
	evs, _, err := src.Fetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := doneText(evs); !ok || got != "Answer B" {
		t.Fatalf("done = %q %v", got, ok)
	}
}

// TestOlderMessageTakenWithoutEditedOnTheWire: Slack may not mark a bot's
// update as edited; the marker alone is enough.
func TestOlderMessageTakenWithoutEditedOnTheWire(t *testing.T) {
	evs, rc := editedEarlierTurn(t,
		func(string) string { return "_Lagi mengurai…_" },
		func(_, tokB string) string { return "The full answer.\nEND RESPONSE " + tokB },
		true,
	)
	if got, ok := doneText(evs); !ok || got != "The full answer." {
		t.Fatalf("turn B done = %q %v, events %+v", got, ok, evs)
	}
	if !rc.Done || rc.Text != "The full answer." {
		t.Fatalf("recheck = %+v", rc)
	}
}

// TestSingleReadWithoutTurnBefore: the first turn, and every turn with the
// marker off, reads one window only, as before.
func TestSingleReadWithoutTurnBefore(t *testing.T) {
	for name, marker := range map[string]*bool{"marker on": nil, "marker off": new(bool)} {
		t.Run(name, func(t *testing.T) {
			f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", Marker: marker})
			f.filter = true
			ctx := context.Background()
			dir := t.TempDir()
			h, err := src.Send(ctx, remote.Turn{Text: "one", SessionDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := src.Fetch(ctx, h); err != nil {
				t.Fatal(err)
			}
			src.End(h)
			f.mu.Lock()
			first := len(f.reads)
			f.mu.Unlock()
			if first != 1 {
				t.Fatalf("first turn read %d windows", first)
			}
			if h, err = src.Send(ctx, remote.Turn{Text: "two", SessionDir: dir}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := src.Fetch(ctx, h); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			second := len(f.reads) - first
			f.mu.Unlock()
			if want := map[bool]int{true: 2, false: 1}[marker == nil]; second != want {
				t.Fatalf("second turn read %d windows, want %d", second, want)
			}
		})
	}
}

// TestDMOlderWindow: in a DM the older window is read from the thread and
// the channel's history alike.
func TestDMOlderWindow(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetDM, User: "UBOT"})
	f.filter = true
	ctx := context.Background()
	dir := t.TempDir()
	sendTurn(t, f, src, dir, "question A")
	note := f.reply("UBOT", "_Lagi mengurai…_")
	src.End(remote.Handle{})
	h, tokB := sendTurn(t, f, src, dir, "question B")
	f.mu.Lock()
	for _, m := range f.replies {
		if m["ts"] == note {
			m["text"] = "DM answer.\nEND RESPONSE " + tokB
		}
	}
	f.reads = nil
	f.mu.Unlock()
	evs, _, err := src.Fetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := doneText(evs); !ok || got != "DM answer." {
		t.Fatalf("done = %q %v, events %+v", got, ok, evs)
	}
	f.mu.Lock()
	reads := len(f.reads)
	f.mu.Unlock()
	if reads != 4 {
		t.Fatalf("reads = %d, want 4 (replies + history, two windows)", reads)
	}
}

// TestOlderWindowErrorKeepsNewest: a failed read of the older window (a
// 429) does not throw away the newest window's reply in a live turn;
// Recheck reports it.
func TestOlderWindowErrorKeepsNewest(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1"})
	f.filter = true
	ctx := context.Background()
	dir := t.TempDir()
	sendTurn(t, f, src, dir, "question A")
	src.End(remote.Handle{})
	h, tokB := sendTurn(t, f, src, dir, "question B")
	f.reply("UBOT", "Answer B\nEND RESPONSE "+tokB)
	f.mu.Lock()
	f.failOld = true
	f.mu.Unlock()
	evs, _, err := src.Fetch(ctx, h)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if got, ok := doneText(evs); !ok || got != "Answer B" {
		t.Fatalf("done = %q %v", got, ok)
	}
	src.End(h)
	if _, err := src.Recheck(ctx, dir); err == nil {
		t.Fatal("recheck hid the failed read")
	}
}

// TestHalfLegacyState: state written before the token and the turn before
// were kept still works — Recheck by the marker's prefix, and the next
// turn reads both windows.
func TestHalfLegacyState(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1"})
	f.filter = true
	ctx := context.Background()
	dir := t.TempDir()
	sendTurn(t, f, src, dir, "question A")
	src.End(remote.Handle{})
	st := LoadState(dir)
	st.Token, st.PrevSentTS = "", ""
	saveState(dir, st)
	f.reply("UBOT", "Answer A\nEND RESPONSE 0123456789ab")
	rc, err := src.Recheck(ctx, dir)
	if err != nil || !rc.Done || rc.Text != "Answer A" {
		t.Fatalf("recheck = %+v %v", rc, err)
	}
	h, tokB := sendTurn(t, f, src, dir, "question B")
	if got := LoadState(dir); got.PrevSentTS != st.SentTS || got.Token != tokB {
		t.Fatalf("state = %+v", got)
	}
	f.reply("UBOT", "Answer B\nEND RESPONSE "+tokB)
	evs, _, err := src.Fetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := doneText(evs); got != "Answer B" {
		t.Fatalf("done = %q", got)
	}
}

// TestRecheckIgnoresAnotherTurnsMarker: with the token kept, a reply
// ending with another turn's marker does not end this one.
func TestRecheckIgnoresAnotherTurnsMarker(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1"})
	f.filter = true
	ctx := context.Background()
	dir := t.TempDir()
	sendTurn(t, f, src, dir, "question")
	src.End(remote.Handle{})
	f.reply("UBOT", "Something\nEND RESPONSE 0123456789ab")
	rc, err := src.Recheck(ctx, dir)
	if err != nil || rc.Done {
		t.Fatalf("recheck = %+v %v", rc, err)
	}
}

// TestOlderMessageIgnoredWithMarkerOff: with the marker off, the remote's
// message from turn A edited after turn B was sent is not turn B's — not
// through the event, not through a read (one window only, after our
// message), not through Recheck — while a reply after turn B still is.
func TestOlderMessageIgnoredWithMarkerOff(t *testing.T) {
	f, src, rt := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", Marker: new(bool)})
	f.filter = true
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := src.Send(ctx, remote.Turn{Text: "question A", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	thread := f.posts[0]["ts"]
	note := f.reply("UBOT", "_Lagi mengurai…_")
	rt.Dispatch(Message{Channel: "C1", TS: note, ThreadTS: thread, User: "UBOT", Text: "_Lagi mengurai…_"})
	src.End(remote.Handle{})
	h, err := src.Send(ctx, remote.Turn{Text: "question B", SessionDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.posts[1]["text"], MarkerPrefix) {
		t.Fatalf("marker off but posted %q", f.posts[1]["text"])
	}
	sentB := f.posts[1]["ts"]
	ch, err := src.Receive(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	edit := f.nextTS()
	final := "The full answer.\nEND RESPONSE 0123456789ab"
	f.mu.Lock()
	for _, m := range f.replies {
		if m["ts"] == note {
			m["text"], m["edited"] = final, map[string]any{"ts": edit}
		}
	}
	f.reads = nil
	f.mu.Unlock()
	rt.Dispatch(changed(thread, note, edit, final))
	if evs := drain(ch); len(evs) != 0 {
		t.Fatalf("event path took the edit: %+v", evs)
	}
	// positive control: the event path does carry a reply after turn B
	reply := f.reply("UBOT", "Answer B")
	rt.Dispatch(Message{Channel: "C1", TS: reply, ThreadTS: thread, User: "UBOT", Text: "Answer B"})
	if got := lastText(drain(ch)); got != "Answer B" {
		t.Fatalf("event path reply = %q, want %q", got, "Answer B")
	}
	evs, _, err := src.Fetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	// the read holds the reply already taken, never the old edit
	for _, ev := range evs {
		if strings.Contains(ev.Text, "The full answer") {
			t.Fatalf("read took the edit: %+v", evs)
		}
	}
	if _, ok := doneText(evs); ok {
		t.Fatalf("read ended the turn: %+v", evs)
	}
	f.mu.Lock()
	reads := strings.Join(f.reads, " ")
	f.mu.Unlock()
	if reads != sentB+".." {
		t.Fatalf("turn B read %q, want only %q", reads, sentB+"..")
	}
	src.End(h)
	rc, err := src.Recheck(ctx, dir)
	if err != nil || rc.Done || rc.Text != "Answer B" {
		t.Fatalf("recheck = %+v %v", rc, err)
	}
}
