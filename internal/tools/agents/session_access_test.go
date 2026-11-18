package agents

import (
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
