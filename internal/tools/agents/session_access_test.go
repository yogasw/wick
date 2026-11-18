package agents

import (
	"os"
	"path/filepath"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/connectors"
)

func TestSessionConnectorPolicyFile(t *testing.T) {
	prev := globalLayout
	t.Cleanup(func() { globalLayout = prev })
	globalLayout = agentconfig.Layout{BaseDir: t.TempDir()}
	sessionPolicyCache.Delete("sess-a")
	if got := SessionConnectorPolicy("sess-a", "c1"); got != connectors.SessionPolicyAllow {
		t.Fatalf("default = %q", got)
	}
	if err := saveSessionPolicy("sess-a", "c1", connectors.SessionPolicyBlock, "u1"); err != nil {
		t.Fatal(err)
	}
	if got := SessionConnectorPolicy("sess-a", "c1"); got != connectors.SessionPolicyBlock {
		t.Fatalf("after block = %q", got)
	}
	// Survives a cold cache (read back from the file).
	sessionPolicyCache.Delete("sess-a")
	if got := SessionConnectorPolicy("sess-a", "c1"); got != connectors.SessionPolicyBlock {
		t.Fatalf("from file = %q", got)
	}
	if got := SessionConnectorPolicy("sess-b", "c1"); got != connectors.SessionPolicyAllow {
		t.Fatalf("other session = %q", got)
	}
	_ = saveSessionPolicy("sess-a", "c1", connectors.SessionPolicyAllow, "u1")
	if got := SessionConnectorPolicy("sess-a", "c1"); got != connectors.SessionPolicyAllow {
		t.Fatalf("back to allow = %q", got)
	}
	// The policy file is not listed as a request.
	if h := sessionAccessHistory("sess-a", 10); len(h) != 0 {
		t.Fatalf("history lists the policy file: %v", h)
	}
	if got := SessionConnectorPolicy("../x", "c1"); got != connectors.SessionPolicyAllow {
		t.Fatal("unsafe session id read a policy")
	}
}

func TestSessionLastAskerAndForget(t *testing.T) {
	prev := globalLayout
	t.Cleanup(func() { globalLayout = prev })
	globalLayout = agentconfig.Layout{BaseDir: t.TempDir()}
	if SessionLastAsker("sess-x") != nil {
		t.Fatal("no conversation must name nobody")
	}
	path := globalLayout.SessionConversation("sess-x")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"role":"user","text":"hi","sender":{"id":"U01TESTUSER","name":"Person One","channel":"slack"}}
{"role":"assistant","text":"ok"}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	a := SessionLastAsker("sess-x")
	if a == nil || a.ID != "U01TESTUSER" || a.Channel != "slack" {
		t.Fatalf("asker = %+v", a)
	}
	// A later turn with nobody behind it (a schedule) names nobody.
	_ = os.WriteFile(path, []byte(lines+`{"role":"user","text":"scheduled"}`+"\n"), 0o644)
	if a := SessionLastAsker("sess-x"); a != nil {
		t.Fatalf("schedule turn named %+v", a)
	}

	sessionPolicyCache.Delete("sess-x")
	_ = saveSessionPolicy("sess-x", "sw_1", connectors.SessionPolicyBlock, "u1")
	ForgetSessionConnector("sess-x", "sw_1")
	if got := SessionConnectorPolicy("sess-x", "sw_1"); got != connectors.SessionPolicyAllow {
		t.Fatalf("removed connector still %q", got)
	}
}
