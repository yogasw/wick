package provider

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
)

// killPool records the kills Switch issues.
type killPool struct{ killed chan string }

func (p killPool) Kill(sessionID, agentName string) error {
	p.killed <- sessionID + "/" + agentName
	return nil
}
func (killPool) Send(ctx context.Context, sessionID, agentName, source, role, text string) error {
	return nil
}

func agentState(t *testing.T, layout config.Layout, id string) session.AgentEntry {
	t.Helper()
	loaded, err := session.Load(layout, id)
	if err != nil {
		t.Fatal(err)
	}
	return loaded.Agents[0]
}

func TestApplySwitchFailover(t *testing.T) {
	isolateConfig(t)
	saveSeed(t, Instance{Type: TypeCodex, Name: "codex"})
	saveSeed(t, Instance{Type: TypeCodex, Name: "gemini_flash"})
	saveSeed(t, Instance{Type: TypeClaude, Name: "claude"})
	layout := config.NewLayout(t.TempDir())

	t.Run("manual switch kills and makes the target home", func(t *testing.T) {
		seedSession(t, layout, "s-manual", "main", "claude/claude")
		pool := killPool{killed: make(chan string, 1)}
		if err := Switch(layout, pool, "s-manual", "main", "codex", SwitchOptions{ModelID: "gpt-5"}); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-pool.killed:
			if got != "s-manual/main" {
				t.Fatalf("killed %q", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Switch did not kill the running agent")
		}
		a := agentState(t, layout, "s-manual")
		want := &session.FailoverState{HomeProvider: "codex/codex", HomeModelID: "gpt-5"}
		if !reflect.DeepEqual(a.Failover, want) {
			t.Fatalf("failover = %+v, want %+v", a.Failover, want)
		}
		turns := readConv(t, layout.SessionConversation("s-manual"))
		if len(turns) != 1 || turns[0].Kind != store.KindProviderSwitch {
			t.Fatalf("turns = %+v", turns)
		}
	})

	t.Run("failover keeps home, advances the run, and survives a manual switch", func(t *testing.T) {
		const id = "s-fo"
		seedSession(t, layout, id, "main", "claude/claude")
		if err := session.SetModelID(layout, id, "main", "opus"); err != nil {
			t.Fatal(err)
		}
		var replies []string
		res, err := ApplySwitch(layout, id, "main", "codex/codex", SwitchOptions{
			Failover:       true,
			FailoverReason: "usage limit reached",
			ContinueText:   "please deploy",
			MsgKey:         "k1",
			Reply:          func(s string) { replies = append(replies, s) },
		})
		if err != nil || res.From != "claude/claude" || res.To != "codex/codex" || res.Home != "claude/claude" || res.HistoryCarries || res.Continue != "please deploy" {
			t.Fatalf("ApplySwitch = %+v, %v", res, err)
		}
		a := agentState(t, layout, id)
		st := a.Failover
		if a.Provider != "codex/codex" || st == nil || st.HomeProvider != "claude/claude" || st.HomeModelID != "opus" ||
			!reflect.DeepEqual(st.Tried, []string{"claude/claude"}) || st.Hops != 1 || st.MsgKey != "k1" || st.Since.IsZero() {
			t.Fatalf("after hop 1: provider %q, failover %+v", a.Provider, st)
		}
		since := st.Since
		turns := readConv(t, layout.SessionConversation(id))
		last := turns[len(turns)-1]
		if last.Kind != store.KindProviderFailover || last.Extras["reason"] != "usage limit reached" ||
			last.Extras["continue"] != "please deploy" || last.Extras["home"] != "claude/claude" ||
			last.Extras["from"] != "claude/claude" || last.Extras["to"] != "codex/codex" {
			t.Fatalf("failover turn = %+v", last)
		}
		for _, ev := range last.Events {
			if ev.Type != store.KindProviderFailover {
				t.Fatalf("event type %q", ev.Type)
			}
		}
		if len(replies) != 1 {
			t.Fatalf("replies = %v", replies)
		}

		// Second hop of the same message: home unchanged, tried grows.
		if _, err := ApplySwitch(layout, id, "main", "codex/gemini_flash", SwitchOptions{Failover: true, MsgKey: "k1"}); err != nil {
			t.Fatal(err)
		}
		st = agentState(t, layout, id).Failover
		if st.HomeProvider != "claude/claude" || !reflect.DeepEqual(st.Tried, []string{"claude/claude", "codex/codex"}) ||
			st.Hops != 2 || !st.Since.Equal(since) {
			t.Fatalf("after hop 2: %+v", st)
		}
		// Back-to-back failover notices are all kept.
		if n := len(readConv(t, layout.SessionConversation(id))); n != 2 {
			t.Fatalf("turns after two failovers = %d, want 2", n)
		}

		// A new message starts a new run.
		if _, err := ApplySwitch(layout, id, "main", "codex/codex", SwitchOptions{Failover: true, MsgKey: "k2"}); err != nil {
			t.Fatal(err)
		}
		st = agentState(t, layout, id).Failover
		if st.HomeProvider != "claude/claude" || !reflect.DeepEqual(st.Tried, []string{"codex/gemini_flash"}) || st.Hops != 1 || st.MsgKey != "k2" {
			t.Fatalf("new run: %+v", st)
		}

		// A manual switch resets the home and must not prune the
		// failover notices before it.
		if _, err := ApplySwitch(layout, id, "main", "claude", SwitchOptions{}); err != nil {
			t.Fatal(err)
		}
		st = agentState(t, layout, id).Failover
		if !reflect.DeepEqual(st, &session.FailoverState{HomeProvider: "claude/claude"}) {
			t.Fatalf("after manual: %+v", st)
		}
		turns = readConv(t, layout.SessionConversation(id))
		kinds := []string{}
		for _, tn := range turns {
			kinds = append(kinds, tn.Kind)
		}
		want := []string{store.KindProviderFailover, store.KindProviderFailover, store.KindProviderFailover, store.KindProviderSwitch}
		if !reflect.DeepEqual(kinds, want) {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	})

	t.Run("failover does not prune a manual switch notice", func(t *testing.T) {
		const id = "s-keep-manual"
		seedSession(t, layout, id, "main", "claude/claude")
		if _, err := ApplySwitch(layout, id, "main", "codex", SwitchOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplySwitch(layout, id, "main", "codex/gemini_flash", SwitchOptions{Failover: true, MsgKey: "m"}); err != nil {
			t.Fatal(err)
		}
		turns := readConv(t, layout.SessionConversation(id))
		if len(turns) != 2 || turns[0].Kind != store.KindProviderSwitch || turns[1].Kind != store.KindProviderFailover {
			t.Fatalf("turns = %+v", turns)
		}
		// Home is the manual pick, not the instance before it.
		if st := agentState(t, layout, id).Failover; st.HomeProvider != "codex/codex" || st.Hops != 1 {
			t.Fatalf("failover = %+v", st)
		}
	})

	t.Run("errors return no keys", func(t *testing.T) {
		seedSession(t, layout, "s-err", "main", "codex/codex")
		res, err := ApplySwitch(layout, "s-err", "main", "codex/nope", SwitchOptions{Failover: true})
		if err == nil || !reflect.DeepEqual(res, SwitchResult{}) {
			t.Fatalf("ApplySwitch = %+v, %v", res, err)
		}
		if _, err := ApplySwitch(layout, "s-err", "main", "codex/gemini_flash", SwitchOptions{Failover: true, ReturnHome: true}); err == nil {
			t.Fatal("failover + return home accepted")
		}
		if agentState(t, layout, "s-err").Failover != nil {
			t.Fatal("a refused switch wrote failover state")
		}
	})

	t.Run("H1: history carries only with a resume id the target can read", func(t *testing.T) {
		snap := TurnSnapshot{UserMsg: "please deploy", Text: "half", Produced: true}
		continueFor := func(res SwitchResult) string {
			return ContinueMessage(ContinueInputFrom(res, event.ErrLimit, time.Time{}, snap))
		}
		// Same type, no transcript yet: starts fresh.
		seedSession(t, layout, "s-h1-none", "main", "codex/codex")
		res, err := ApplySwitch(layout, "s-h1-none", "main", "codex/gemini_flash", SwitchOptions{Failover: true, MsgKey: "m", ContinueFor: continueFor})
		if err != nil || res.HistoryCarries {
			t.Fatalf("no transcript: %+v, %v", res, err)
		}
		if !strings.Contains(res.Continue, `Last user message: """please deploy"""`) {
			t.Fatalf("fresh target got no recap:\n%s", res.Continue)
		}
		// Same type with a readable transcript: carries.
		RegisterHistoryCarrier(TypeCodex, func(from, to Instance, resumeID string) HistoryCarry {
			return HistoryCarry{}
		})
		seedSession(t, layout, "s-h1-ok", "main", "codex/codex")
		_ = session.SetCLISessionID(layout, "s-h1-ok", "main", "thread-1")
		res, err = ApplySwitch(layout, "s-h1-ok", "main", "codex/gemini_flash", SwitchOptions{Failover: true, MsgKey: "m", ContinueFor: continueFor})
		if err != nil || !res.HistoryCarries || strings.Contains(res.Continue, "please deploy") {
			t.Fatalf("readable transcript: %+v, %v", res, err)
		}
		// Same type, but the target cannot reach the transcript.
		RegisterHistoryCarrier(TypeCodex, func(from, to Instance, resumeID string) HistoryCarry {
			return HistoryCarry{Reason: "separate store"}
		})
		defer RegisterHistoryCarrier(TypeCodex, nil)
		seedSession(t, layout, "s-h1-sep", "main", "codex/codex")
		_ = session.SetCLISessionID(layout, "s-h1-sep", "main", "thread-2")
		res, err = ApplySwitch(layout, "s-h1-sep", "main", "codex/gemini_flash", SwitchOptions{Failover: true, MsgKey: "m", ContinueFor: continueFor})
		if err != nil || res.HistoryCarries || !strings.Contains(res.Continue, "please deploy") {
			t.Fatalf("unreachable transcript: %+v, %v", res, err)
		}
		turns := readConv(t, layout.SessionConversation("s-h1-sep"))
		if got := turns[len(turns)-1].Extras["continue"]; got != res.Continue {
			t.Fatalf("notice continue = %q, want the ContinueFor text", got)
		}
	})

	t.Run("M1: an empty msg key is a new run every time", func(t *testing.T) {
		const id = "s-m1"
		seedSession(t, layout, id, "main", "claude/claude")
		for i, tag := range []string{"codex/codex", "codex/gemini_flash"} {
			if _, err := ApplySwitch(layout, id, "main", tag, SwitchOptions{Failover: true}); err != nil {
				t.Fatal(err)
			}
			if st := agentState(t, layout, id).Failover; st.Hops != 1 || len(st.Tried) != 1 || st.HomeProvider != "claude/claude" {
				t.Fatalf("hop %d: %+v", i, st)
			}
		}
	})

	t.Run("M2: return home keeps the home and ends the run", func(t *testing.T) {
		const id = "s-ret"
		seedSession(t, layout, id, "main", "claude/claude")
		if _, err := ApplySwitch(layout, id, "main", "codex/codex", SwitchOptions{Failover: true, MsgKey: "k"}); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplySwitch(layout, id, "main", "codex/gemini_flash", SwitchOptions{Failover: true, MsgKey: "k"}); err != nil {
			t.Fatal(err)
		}
		// Up the chain, not yet home: the home must not move.
		res, err := ApplySwitch(layout, id, "main", "codex/codex", SwitchOptions{ReturnHome: true})
		if err != nil || res.Home != "claude/claude" {
			t.Fatalf("return up the chain = %+v, %v", res, err)
		}
		a := agentState(t, layout, id)
		// The stand-ins are remembered until the agent is back home.
		wantSt := &session.FailoverState{HomeProvider: "claude/claude", StandIns: []string{"codex/codex", "codex/gemini_flash"}}
		if a.Provider != "codex/codex" || !reflect.DeepEqual(a.Failover, wantSt) {
			t.Fatalf("after return up the chain: %q %+v", a.Provider, a.Failover)
		}
		res, err = ApplySwitch(layout, id, "main", "claude", SwitchOptions{ReturnHome: true})
		if err != nil || res.To != "claude/claude" || res.Home != "claude/claude" {
			t.Fatalf("return home = %+v, %v", res, err)
		}
		turns := readConv(t, layout.SessionConversation(id))
		kinds := []string{}
		for _, tn := range turns {
			kinds = append(kinds, tn.Kind)
		}
		want := []string{store.KindProviderFailover, store.KindProviderFailover, store.KindProviderReturn, store.KindProviderReturn}
		if !reflect.DeepEqual(kinds, want) {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
		if last := turns[len(turns)-1]; !strings.HasPrefix(last.Events[0].Text, "Back to home claude") || last.Extras["home"] != "claude/claude" {
			t.Fatalf("return notice = %+v", last)
		}
		// codex → claude: the home resumes its own transcript only, so
		// every stand-in passed through is named, not just the last.
		if note := turns[len(turns)-1].Extras["note"]; !strings.Contains(note, "turns answered on codex/codex, codex/gemini_flash while they stood in are not part of claude") {
			t.Fatalf("return note = %q, want every stand-in's turns called out", note)
		}
		if st := agentState(t, layout, id).Failover; len(st.StandIns) != 0 {
			t.Fatalf("back home: stand-ins %v not cleared", st.StandIns)
		}
		if !strings.HasPrefix(turns[2].Events[0].Text, "Moved back up the failover chain of claude/claude") {
			t.Fatalf("chain notice = %q", turns[2].Events[0].Text)
		}
	})
}

func TestChannelLabel(t *testing.T) {
	for _, c := range []struct{ key, model, want string }{
		{"claude/claude_support_ent", "", "claude_support_ent"},
		{"omp/work", "anthropic/claude-opus-4", "work · claude-opus-4"},
		{"codex/codex", "gpt-5", "codex · gpt-5"},
		{"claude", "", "claude"},
	} {
		if got := ChannelLabel(c.key, c.model); got != c.want {
			t.Errorf("ChannelLabel(%q, %q) = %q, want %q", c.key, c.model, got, c.want)
		}
	}
}

// TestFailoverReplyUsesChannelLabels (F4b-T10): the channel Reply of an
// automatic switch names instances by label, not by "type/name" key.
func TestFailoverReplyUsesChannelLabels(t *testing.T) {
	isolateConfig(t)
	saveSeed(t, Instance{Type: TypeCodex, Name: "codex"})
	saveSeed(t, Instance{Type: TypeClaude, Name: "claude"})
	layout := config.NewLayout(t.TempDir())
	const id = "s-lbl"
	seedSession(t, layout, id, "main", "claude/claude")
	var replies []string
	reply := func(s string) { replies = append(replies, s) }
	if _, err := ApplySwitch(layout, id, "main", "codex/codex", SwitchOptions{Failover: true, MsgKey: "k", ModelID: "openai/gpt-5", FailoverReason: "usage limit", Reply: reply}); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySwitch(layout, id, "main", "claude/claude", SwitchOptions{ReturnHome: true, Reply: reply}); err != nil {
		t.Fatal(err)
	}
	if len(replies) != 2 {
		t.Fatalf("replies = %q", replies)
	}
	for _, r := range replies {
		if strings.Contains(r, "claude/claude") || strings.Contains(r, "codex/codex") {
			t.Fatalf("reply carries a raw tag: %q", r)
		}
	}
	if !strings.Contains(replies[0], "Switched to codex · gpt-5") {
		t.Fatalf("failover reply = %q", replies[0])
	}
}
