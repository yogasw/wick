package pool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

type onSendKey struct{}

// heldSpawner spawns a process that starts a turn and never ends it, so
// the session stays live for a second message.
type heldSpawner struct{ scriptedSpawner }

func (s *heldSpawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr, pw := io.Pipe()
	proc := &scriptedProc{stdoutR: pr, stdoutW: pw, stdinBuf: &bytes.Buffer{}, opt: opt, done: make(chan struct{}), pid: 71000 + s.Calls}
	s.Calls++
	s.Procs = append(s.Procs, proc)
	go func() {
		_, _ = pw.Write([]byte(`{"type":"system","subtype":"init","session_id":"abc"}` + "\n"))
	}()
	return proc, nil
}

// OnSend sees every message into a session, with the sender's ctx and
// role, before it is routed: the one that spawns the process and one
// sent to it live. done tells whether each reached the session.
func TestOnSendSeesEveryMessageWithItsCtx(t *testing.T) {
	sp := &heldSpawner{}
	p, layout := newPool(t, 2, &sp.scriptedSpawner)
	p.cfg.Factory.(*ClaudeFactory).Spawner = sp
	setupSession(t, layout, "S1")
	var mu sync.Mutex
	var seen []string
	p.cfg.OnSend = func(ctx context.Context, sessionID, role string) (func(bool), error) {
		v, _ := ctx.Value(onSendKey{}).(string)
		return func(delivered bool) {
			mu.Lock()
			seen = append(seen, fmt.Sprintf("%s:%s:%s:%v", sessionID, v, role, delivered))
			mu.Unlock()
		}, nil
	}
	ctx := context.WithValue(context.Background(), onSendKey{}, "first")
	if err := p.Send(ctx, "S1", "default", "slack", "user", "hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 1 && sp.procCount() == 1 }, 2*time.Second)
	if err := p.Send(context.WithValue(context.Background(), onSendKey{}, "second"), "S1", "default", "ui", "user", "again"); err != nil {
		t.Fatal(err)
	}
	// Live: written to the one running process, nothing spawned for it.
	waitFor(t, func() bool { return strings.Contains(sp.procAt(0).recordedStdin(), "again") }, 2*time.Second)
	if n := sp.procCount(); n != 1 {
		t.Fatalf("spawns = %d, want 1", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "S1:first:user:true" || seen[1] != "S1:second:user:true" {
		t.Fatalf("seen = %v", seen)
	}
}

// An OnSend error refuses the message: Send returns it and nothing is
// buffered or spawned.
func TestOnSendErrorRefusesTheMessage(t *testing.T) {
	sp := scriptedOK()
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")
	p.cfg.OnSend = func(ctx context.Context, sessionID, role string) (func(bool), error) {
		return nil, context.Canceled
	}
	if err := p.Send(context.Background(), "S1", "default", "ui", "user", "hi"); err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := sp.callsSnapshot(); n != 0 {
		t.Fatalf("spawns = %d, want 0", n)
	}
}
