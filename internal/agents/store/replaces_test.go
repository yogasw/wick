package store

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
)

// tickingStore is a store whose clock moves a second per reading, so each
// turn gets its own id.
func tickingStore(t *testing.T) (*Store, config.Layout) {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "S1", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	clk := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)
	return New(Options{Layout: layout, SessionID: "S1", AgentName: "main", Now: func() time.Time {
		clk = clk.Add(time.Second)
		return clk
	}}), layout
}

// A remote's late reply takes the place of the timeout it follows.
func TestLateReplyReplacesTimeoutTurn(t *testing.T) {
	st, layout := tickingStore(t)
	if _, err := st.Apply(event.AgentEvent{Type: event.Error, ErrorMsg: "No reply from the remote agent after 3m0s."}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "Final answer."}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(event.AgentEvent{Type: event.Done, RemoteNote: remoteNoteLate}); err != nil {
		t.Fatal(err)
	}
	turns := readConvLines(t, layout)
	if len(turns) != 2 || !turns[0].IsError {
		t.Fatalf("turns = %+v", turns)
	}
	if turns[1].Replaces != turns[0].TurnID || turns[1].Text != "Final answer." {
		t.Fatalf("late reply = %+v, want it to replace %s", turns[1], turns[0].TurnID)
	}
}

// Only a late reply replaces, and only the timeout no message followed.
func TestOnlyLateReplyReplacesTimeout(t *testing.T) {
	st, layout := tickingStore(t)
	_, _ = st.Apply(event.AgentEvent{Type: event.Error, ErrorMsg: "No reply from the remote agent after 3m0s."})
	if err := st.AppendUserTurn("user", "ui", "new question"); err != nil {
		t.Fatal(err)
	}
	_, _ = st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "late"})
	_, _ = st.Apply(event.AgentEvent{Type: event.Done, RemoteNote: remoteNoteLate})
	_, _ = st.Apply(event.AgentEvent{Type: event.Error, ErrorMsg: "some other failure"})
	_, _ = st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "late again"})
	_, _ = st.Apply(event.AgentEvent{Type: event.Done, RemoteNote: remoteNoteLate})
	for _, tr := range readConvLines(t, layout) {
		if tr.Replaces != "" {
			t.Fatalf("turn %q replaces %s, want none", tr.Text, tr.Replaces)
		}
	}
}
