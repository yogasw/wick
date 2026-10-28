package slack

import (
	"encoding/json"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// boundListenSessions is fakeListenSessions with every session bound to one
// channel thread.
type boundListenSessions struct {
	*fakeListenSessions
	chat string
}

func (b boundListenSessions) ThreadBinding(string) (agentchannels.ThreadBinding, bool) {
	return agentchannels.ThreadBinding{Channel: "slack", ChatID: b.chat, ThreadID: "100.1"}, true
}

func replyModeChannel(t *testing.T, cfg agentconfig.SlackChannelConfig) (*Channel, *fakeListenSessions) {
	t.Helper()
	fake := newFakeListen("")
	ch := New(cfg)
	ch.sessions = boundListenSessions{fakeListenSessions: fake, chat: "C01TESTCHAN"}
	return ch, fake
}

func TestWithChannelRuleMode(t *testing.T) {
	got, err := withChannelRuleMode(`[{"id":"C01TESTCHAN","name":"general","mode":"listen","delay":"9"}]`, "C01TESTCHAN", "x", replyModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	var rows []replyRule
	_ = json.Unmarshal([]byte(got), &rows)
	if len(rows) != 1 || rows[0].Mode != replyModeAuto || rows[0].Delay != "9" || rows[0].Name != "general" {
		t.Fatalf("row not updated in place: %s", got)
	}
	got, _ = withChannelRuleMode("", "C02OTHER", "random", replyModeListen)
	if got != `[{"id":"C02OTHER","name":"random","mode":"listen","delay":""}]` {
		t.Fatalf("new row = %s", got)
	}
	if _, err := withChannelRuleMode("{", "C1", "", replyModeAuto); err == nil {
		t.Fatal("broken rules accepted")
	}
}

func TestReplyModeSwitch(t *testing.T) {
	cfg := agentconfig.SlackChannelConfig{ReactionTriggerEnabled: true, ReactionChannelsMode: "all",
		ChannelReplyRules: `[{"id":"C01TESTCHAN","name":"general","mode":"listen","delay":""}]`}
	ch, fake := replyModeChannel(t, cfg)

	st, _ := ch.ThreadListen("S", ListenRequest{Action: "status"})
	if st.Mode != replyModeManual || st.ChannelMode != replyModeListen || st.ChannelName != "general" {
		t.Fatalf("not listening yet: %+v", st)
	}

	// Listen on a listen channel: only the thread changes.
	rules, changed, err := ch.ReplyModeRules("S", "listen")
	if err != nil || changed {
		t.Fatalf("listen on a listen channel: changed=%v err=%v", changed, err)
	}
	fake.SetListenThread("S", agentchannels.ListenThread{Prompt: "only billing"})
	st, _ = ch.ApplyReplyMode("S", "listen", rules, changed)
	if st.Mode != replyModeListen || fake.ListenThread("S").Prompt != "only billing" {
		t.Fatalf("listen: %+v thread=%+v", st, fake.ListenThread("S"))
	}

	// Auto-reply changes the channel's rule, at once.
	rules, changed, err = ch.ReplyModeRules("S", "auto_reply")
	if err != nil || !changed {
		t.Fatalf("auto on a listen channel: changed=%v err=%v", changed, err)
	}
	st, _ = ch.ApplyReplyMode("S", "auto_reply", rules, changed)
	if st.Mode != replyModeAuto || st.ChannelMode != replyModeAuto || st.HoldSec != 3 {
		t.Fatalf("auto: %+v", st)
	}

	// Manual stops this thread only; the channel stays auto-reply.
	rules, changed, _ = ch.ReplyModeRules("S", "manual")
	if changed {
		t.Fatal("manual touched the channel rule")
	}
	st, _ = ch.ApplyReplyMode("S", "manual", rules, changed)
	if st.Mode != replyModeManual || st.ChannelMode != replyModeAuto || !fake.ListenThread("S").Stopped {
		t.Fatalf("manual: %+v", st)
	}

	if _, _, err := ch.ReplyModeRules("S", "loud"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestReplyModeRefusesDM(t *testing.T) {
	ch := New(agentconfig.SlackChannelConfig{})
	ch.sessions = boundListenSessions{fakeListenSessions: newFakeListen(""), chat: "D01TESTDM"}
	if _, _, err := ch.ReplyModeRules("S", "listen"); err == nil {
		t.Fatal("a DM got a reply mode")
	}
}
