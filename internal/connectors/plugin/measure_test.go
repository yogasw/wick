package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// BenchmarkWarmExecute measures per-call latency through a warm plugin
// subprocess (the first call warms the spawn; the timed loop is steady-state
// IPC + the no-op echo op, with no upstream API). Run:
//
//	go test ./internal/connectors/plugin/ -bench BenchmarkWarmExecute -benchmem -run x
func BenchmarkWarmExecute(b *testing.B) {
	dir := buildEcho(b)
	found, err := Scan(dir)
	if err != nil || len(found) != 1 {
		b.Fatalf("scan: %v %d", err, len(found))
	}
	mgr := NewManager(map[string]string{found[0].Key: found[0].BinaryPath}, 5*time.Minute)
	defer mgr.KillAll()
	mod := BuildModule(found[0].Manifest.Module, mgr.Client)
	op := mod.AllOps()[0]
	cctx := connector.NewPluginCtx(context.Background(),
		map[string]string{"token": "x"}, map[string]string{"text": "hi"})
	if _, err := op.Execute(cctx); err != nil { // warm spawn
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := op.Execute(cctx); err != nil {
			b.Fatal(err)
		}
	}
}
