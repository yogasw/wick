package agentctl

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// roundtrip drives one Cmd through Server.handle over an in-memory
// pipe and returns the Reply.
func roundtrip(t *testing.T, s *Server, cmd Cmd) Reply {
	t.Helper()
	client, server := net.Pipe()
	go s.handle(server)
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(client).Encode(cmd); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var rep Reply
	if err := json.NewDecoder(client).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return rep
}

func TestRefreshSessionInvokesCallback(t *testing.T) {
	got := ""
	s := NewServer(nil, agentconfig.Layout{}).WithRefresh(func(id string) {
		got = id
	})
	rep := roundtrip(t, s, Cmd{Op: OpRefreshSession, SessionID: "sess-42"})
	if !rep.OK {
		t.Fatalf("reply not OK: %+v", rep)
	}
	if got != "sess-42" {
		t.Fatalf("onRefresh got %q, want sess-42", got)
	}
}

func TestRefreshSessionRequiresID(t *testing.T) {
	s := NewServer(nil, agentconfig.Layout{}).WithRefresh(func(string) {})
	rep := roundtrip(t, s, Cmd{Op: OpRefreshSession})
	if rep.OK || rep.Error == "" {
		t.Fatalf("expected error for missing session_id, got %+v", rep)
	}
}

func TestRefreshSessionNilCallbackStillOK(t *testing.T) {
	s := NewServer(nil, agentconfig.Layout{}) // no WithRefresh
	rep := roundtrip(t, s, Cmd{Op: OpRefreshSession, SessionID: "x"})
	if !rep.OK {
		t.Fatalf("nil callback should still reply OK, got %+v", rep)
	}
}
