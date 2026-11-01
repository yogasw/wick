package slack

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack/slackevents"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

const rulesJSON = `[{"id":"CAUTO","name":"#debug","mode":"auto_reply","delay":"1"},` +
	`{"id":"CLIS","name":"#ops","mode":"listen","delay":"40"},` +
	`{"id":"CMAN","name":"#client","mode":"manual","delay":""}]`

func TestReplyModeFor(t *testing.T) {
	cfg := agentconfig.SlackChannelConfig{ChannelReplyRules: rulesJSON}
	for ch, want := range map[string]string{"CAUTO": replyModeAuto, "CLIS": replyModeListen, "CMAN": replyModeManual, "COTHER": replyModeListen} {
		if got := replyModeFor(cfg, ch); got != want {
			t.Errorf("%s: mode %q, want %q", ch, got, want)
		}
	}
	cfg.ReplyMode = "auto_reply"
	if got := replyModeFor(cfg, "COTHER"); got != replyModeAuto {
		t.Errorf("bot-wide auto_reply not applied: %q", got)
	}
	if got := replyModeFor(cfg, "CLIS"); got != replyModeListen {
		t.Errorf("channel rule must win over the default: %q", got)
	}
	if !listenManual(cfg, "CMAN") || listenManual(cfg, "CLIS") {
		t.Error("a manual rule must count as a listen-manual channel, a listen rule must not")
	}
	if got := replyModeFor(agentconfig.SlackChannelConfig{ChannelReplyRules: "not json"}, "CAUTO"); got != replyModeListen {
		t.Errorf("broken rules must fall back to listen, got %q", got)
	}
}

func TestAutoReplyDelay(t *testing.T) {
	cfg := agentconfig.SlackChannelConfig{ChannelReplyRules: rulesJSON}
	if d := autoReplyDelay(cfg, "CAUTO"); d != time.Second {
		t.Errorf("rule delay: %v", d)
	}
	if d := autoReplyDelay(cfg, "COTHER"); d != defaultAutoReplyDelay {
		t.Errorf("default delay: %v", d)
	}
	cfg.AutoReplyDelaySec = "0"
	if d := autoReplyDelay(cfg, "COTHER"); d != 0 {
		t.Errorf("setting 0 must mean at once: %v", d)
	}
	cfg.AutoReplyDelaySec = "600"
	if d := autoReplyDelay(cfg, "COTHER"); d != autoReplyMaxHold {
		t.Errorf("delay must be capped at %v: %v", autoReplyMaxHold, d)
	}
}

// The thread's request beats the channel rule, which beats the agent's
// LISTEN.md, which beats the bot-wide setting, which beats the wick doc.
func TestListenDelayPrecedence(t *testing.T) {
	wick := "---\ndelay: 15s\n---\n"
	agent := "---\ndelay: 20s\n---\n"
	d := func(s int) *time.Duration { v := time.Duration(s) * time.Second; return &v }
	cases := []struct {
		name  string
		agent string
		ov    listenOverrides
		lt    agentchannels.ListenThread
		want  time.Duration
	}{
		{"wick only", "", listenOverrides{}, agentchannels.ListenThread{}, 15 * time.Second},
		{"setting over wick", "", listenOverrides{cfg: d(5)}, agentchannels.ListenThread{}, 5 * time.Second},
		{"agent over setting", agent, listenOverrides{cfg: d(5)}, agentchannels.ListenThread{}, 20 * time.Second},
		{"channel over agent", agent, listenOverrides{cfg: d(5), channel: d(40)}, agentchannels.ListenThread{}, 40 * time.Second},
		{"thread over channel", agent, listenOverrides{channel: d(40)}, agentchannels.ListenThread{DelaySec: func() *int { v := 2; return &v }()}, 2 * time.Second},
	}
	for _, c := range cases {
		if got := resolveListenWith(wick, c.agent, c.lt, c.ov).Delay; got != c.want {
			t.Errorf("%s: delay %v, want %v", c.name, got, c.want)
		}
	}
}

type heldRec struct {
	mu   sync.Mutex
	rows []map[string]string
}

func (h *heldRec) fn(_ string, _ string, extras map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rows = append(h.rows, extras)
}

func (h *heldRec) states() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range h.rows {
		out = append(out, r["state"])
	}
	return out
}

func modeChannel(t *testing.T, cfg agentconfig.SlackChannelConfig) (*Channel, *heldRec, chan [3]any) {
	t.Helper()
	ch := New(cfg)
	ch.sessions = newFakeListen("")
	rec := &heldRec{}
	ch.SetHeldFn(rec.fn)
	got := make(chan [3]any, 4)
	ch.listenDispatchHook = func(_ string, ev *slackevents.MessageEvent, waited time.Duration, others bool) {
		got <- [3]any{ev.Text, waited, others}
	}
	return ch, rec, got
}

func msgIn(channel, user, text, ts string) *slackevents.MessageEvent {
	return &slackevents.MessageEvent{User: user, Text: text, Channel: channel, ThreadTimeStamp: "100.1", TimeStamp: ts}
}

// In an auto-reply channel a burst from several people is one turn, and
// someone else talking never marks the hold as answered.
func TestAutoReplyMergesBurst(t *testing.T) {
	ch, rec, got := modeChannel(t, agentconfig.SlackChannelConfig{ChannelReplyRules: rulesJSON})
	ch.queueListenMessage(t.Context(), "S", msgIn("CAUTO", "UA", "first", "100.2"), nil)
	ch.queueListenMessage(t.Context(), "S", msgIn("CAUTO", "UB", "second", "100.3"), nil)
	select {
	case r := <-got:
		if r[0] != "second" || r[2] != false {
			t.Fatalf("dispatched %v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("merged reply never went through")
	}
	select {
	case r := <-got:
		t.Fatalf("burst dispatched twice: %v", r)
	case <-time.After(300 * time.Millisecond):
	}
	if s := strings.Join(rec.states(), ","); s != "held,held,sent" {
		t.Fatalf("held states %q", s)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.rows[0]["hold_id"] == "" || rec.rows[0]["hold_id"] != rec.rows[2]["hold_id"] || rec.rows[0]["mode"] != replyModeAuto {
		t.Fatalf("one hold must keep one hold_id and its mode: %v", rec.rows)
	}
}

// A listen hold is reported the moment it starts, and a cancel says so.
func TestListenHeldReportedAndCancelled(t *testing.T) {
	ch, rec, got := modeChannel(t, agentconfig.SlackChannelConfig{ChannelReplyRules: rulesJSON})
	ch.queueListenMessage(t.Context(), "S", msgIn("CLIS", "UA", "halo", "100.2"), nil)
	if s := strings.Join(rec.states(), ","); s != "held" {
		t.Fatalf("hold not reported at once: %q", s)
	}
	rec.mu.Lock()
	delay := rec.rows[0]["delay_sec"]
	rec.mu.Unlock()
	if delay != "40" {
		t.Fatalf("channel rule delay not used: %q", delay)
	}
	ch.cancelListenWait("S")
	if s := strings.Join(rec.states(), ","); s != "held,cancelled" {
		t.Fatalf("cancel not reported: %q", s)
	}
	select {
	case r := <-got:
		t.Fatalf("cancelled hold dispatched %v", r)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestListenNoteAutoReply(t *testing.T) {
	n := listenNote(listenTurn{auto: true, waited: 3 * time.Second})
	if !strings.HasPrefix(n, "[auto-reply · held 3s]") || !strings.Contains(n, "never [silent]") {
		t.Fatalf("auto-reply note: %q", n)
	}
	if strings.Contains(n, "[listen mode") {
		t.Fatalf("auto-reply note must not read as listen: %q", n)
	}
}
