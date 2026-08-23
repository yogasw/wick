package mcp

import (
	"testing"

	"github.com/yogasw/wick/internal/mcp/handlers"
)

// TestInternalAgentUserIDMatchesHandlers pins the duplicated constant. The
// handlers package cannot import internal/mcp (that would be an import cycle),
// so it repeats the synthetic-admin id locally for wick_me's is_system flag.
// If the two ever drift, wick_me would report a real user as a system
// principal, or vice versa.
func TestInternalAgentUserIDMatchesHandlers(t *testing.T) {
	if got := handlers.InternalAgentUserIDForTest(); got != InternalAgentUserID {
		t.Fatalf("handlers copy = %q, mcp.InternalAgentUserID = %q — keep them equal",
			got, InternalAgentUserID)
	}
}
