package omp

import (
	"bufio"
	"strings"
	"testing"
)

// The idle timer asks the process before killing a silent turn: omp's
// get_state says whether the session is still working — streaming,
// compacting, or holding async work such as a background sub-agent.
func TestRPCBusyFollowsGetState(t *testing.T) {
	f := &fakeOMP{hang: true}
	p, m := runFakeTurn(t, f, "long")
	defer m.Shutdown()
	if p.Busy() {
		t.Fatal("busy before the prompt went out")
	}
	r := bufio.NewReader(p.Stdout())
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, "echo:long") {
			break
		}
	}
	for _, tc := range []struct {
		state map[string]any
		want  bool
	}{
		{map[string]any{"isStreaming": true}, true},
		{map[string]any{"isStreaming": false, "isCompacting": true}, true},
		{map[string]any{"isStreaming": false, "hasPendingAsyncWork": true}, true},
		{map[string]any{"isStreaming": false}, false},
		{nil, false}, // an omp that does not say is not busy
	} {
		f.mu.Lock()
		f.state = tc.state
		f.mu.Unlock()
		if got := p.Busy(); got != tc.want {
			t.Fatalf("state %v: Busy = %v, want %v", tc.state, got, tc.want)
		}
	}
	f.mu.Lock()
	f.state = map[string]any{"isStreaming": true}
	f.mu.Unlock()
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = p.Wait()
	if p.Busy() {
		t.Fatal("a killed turn reported busy")
	}
}
