package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/accessreq"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

func TestAccessFileHistoryPerSessionAndProjectIndex(t *testing.T) {
	l := agentconfig.Layout{BaseDir: t.TempDir()}
	p := AccessFilePublisher{Layout: l, Project: func(sid string) string {
		if sid == "sess-a" || sid == "sess-b" {
			return "proj-1"
		}
		return ""
	}}
	ctx := context.Background()
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	pub := func(id, sid, status string, at time.Time) {
		t.Helper()
		v := accessreq.View{ID: id, SessionID: sid, Status: status, CreatedAt: at,
			Items: []accessreq.ItemView{{ID: id + "-i", ConnectorKey: "notes", OpKey: "update_page", Status: status}}}
		if _, err := p.Publish(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	pub("r1", "sess-a", "pending", t0)
	pub("r1", "sess-a", "done", t0) // a change rewrites, no second index line
	pub("r2", "sess-b", "pending", t0.Add(time.Minute))
	pub("r3", "sess-x", "pending", t0) // no project: session file only

	if _, err := os.Stat(filepath.Join(l.SessionDir("sess-x"), "access", "r3.json")); err != nil {
		t.Fatalf("session without project has no history file: %v", err)
	}
	idx, _ := os.ReadFile(accessIndexFile(l, "proj-1"))
	if n := strings.Count(string(idx), "\n"); n != 2 {
		t.Fatalf("index lines = %d, want 2:\n%s", n, idx)
	}
	got := projectAccessHistory(l, "proj-1", 50)
	if len(got) != 2 || got[0]["id"] != "r2" || got[1]["id"] != "r1" || got[1]["status"] != "done" {
		t.Fatalf("history = %v", got)
	}
	if got := projectAccessHistory(l, "proj-1", 1); len(got) != 1 {
		t.Fatalf("limit ignored: %d", len(got))
	}
	if got := projectAccessHistory(l, "../proj-1", 50); len(got) != 0 {
		t.Fatal("path escape read a history")
	}
	if _, err := p.Publish(ctx, accessreq.View{ID: "../evil", SessionID: "sess-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(l.SessionDir("sess-a"), "evil.json")); err == nil {
		t.Fatal("unsafe request id wrote outside the access folder")
	}
}
