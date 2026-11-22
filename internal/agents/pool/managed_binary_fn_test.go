package pool

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// Only a wick-managed binary is re-resolved per spawn: a manual path or a
// PATH lookup does not move, so it keeps the path fixed at build.
func TestManagedBinaryFnOnlyForManaged(t *testing.T) {
	for _, src := range []string{"registry", "path", "scan", "unconfigured"} {
		if managedBinaryFn(src, "omp", "omp") != nil {
			t.Errorf("source %q got a re-resolver", src)
		}
	}
	if managedBinaryFn(provider.BinSourceManaged, "omp", "omp") == nil {
		t.Error("managed source got no re-resolver")
	}
}
