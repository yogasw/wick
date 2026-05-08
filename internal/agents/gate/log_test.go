package gate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
)

func TestAppendRoundtrip(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID:     "S1",
		Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}

	entries := []Entry{
		{Agent: "backend", Cmd: "git status", Status: "allowed"},
		{Agent: "backend", Cmd: "rm -rf .", Status: "blocked", Reason: "no matching"},
	}
	for _, e := range entries {
		if err := Append(layout, "S1", e); err != nil {
			t.Fatal(err)
		}
	}

	var got []Entry
	if err := storage.ReadJSONL(layout.SessionCommands("S1"), func(line []byte) bool {
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		got = append(got, e)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("entries: got %d, want 2", len(got))
	}
	if got[0].Cmd != "git status" || got[0].Status != "allowed" {
		t.Fatalf("entry 0: %+v", got[0])
	}
	if got[1].Cmd != "rm -rf ." || got[1].Status != "blocked" || got[1].Reason != "no matching" {
		t.Fatalf("entry 1: %+v", got[1])
	}
	if got[0].Timestamp.IsZero() {
		t.Fatal("ts auto-fill missing")
	}
}
