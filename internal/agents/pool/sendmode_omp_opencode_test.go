package pool

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// omp/opencode run one process per turn, like codex: a mid-turn message
// must queue, not stack a second process.
func TestSendModeForOneShotCLIs(t *testing.T) {
	for _, ty := range []provider.Type{provider.TypeOMP, provider.TypeOpencode} {
		if got := sendModeFor(ty, ""); got != provider.SendRespawnQueue {
			t.Errorf("%s default = %v, want queue", ty, got)
		}
	}
	if got := sendModeFor(provider.TypeGemini, ""); got != provider.SendAppend {
		t.Errorf("gemini default changed: %v", got)
	}
}

// "append" on a CLI that reads its prompt once would swallow every
// mid-turn message; it resolves to queue-and-combine instead.
func TestSendModeAppendOverrideOnOneShotQueues(t *testing.T) {
	for _, ty := range []provider.Type{provider.TypeCodex, provider.TypeOMP, provider.TypeOpencode} {
		if got := sendModeFor(ty, "append"); got != provider.SendRespawnQueue {
			t.Errorf("%s append = %v, want queue", ty, got)
		}
	}
	if got := sendModeFor(provider.TypeClaude, "append"); got != provider.SendAppend {
		t.Errorf("claude append = %v, want append", got)
	}
}
