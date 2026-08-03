package subagents

import (
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/entity"
)

// The MCP op and the web handler must refuse a locked role for the same
// reason and with the same words. Both go through delegation.CheckMutable,
// so this test pins the contract the handler relies on rather than
// re-deriving it here.
func TestCreateAgentRefusesLockedRole(t *testing.T) {
	err := delegation.CheckMutable(&entity.AgentProfile{Key: "reviewer", Locked: true})
	if !errors.Is(err, delegation.ErrProfileLocked) {
		t.Fatalf("err = %v, want ErrProfileLocked", err)
	}
}

// The ratchet: an agent may freeze a role, never thaw one. Once Locked is
// true, CheckMutable stops every later create_agent call — including one
// that sends locked=false — so there is no MCP path back to an editable
// role.
func TestLockIsOneWayFromMCP(t *testing.T) {
	frozen := &entity.AgentProfile{Key: "reviewer", Locked: true}
	if err := delegation.CheckMutable(frozen); err == nil {
		t.Fatal("MCP can still mutate a locked role")
	}
}
