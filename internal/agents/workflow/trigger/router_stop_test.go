package trigger

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// TestRouterStop_DrainsCleanly verifies Stop returns promptly when all
// workers finish before the deadline.
func TestRouterStop_DrainsCleanly(t *testing.T) {
	r := &Router{
		workers: map[string]context.CancelFunc{},
		index:   map[string][]triggerRef{},
		defs:    map[string]workflow.Workflow{},
		queues:  map[string]*Queue{},
		dedups:  map[string]*Dedup{},
		clock:   time.Now,
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		time.Sleep(10 * time.Millisecond)
	}()

	orig := StopTimeout
	StopTimeout = 2 * time.Second
	defer func() { StopTimeout = orig }()

	start := time.Now()
	r.Stop()

	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("Stop() took too long (%v); expected fast drain", elapsed)
	}
}

// TestRouterStop_TimesOutOnStuckWorker verifies Stop does not hang
// forever when a worker is stuck — it returns after StopTimeout.
func TestRouterStop_TimesOutOnStuckWorker(t *testing.T) {
	r := &Router{
		workers: map[string]context.CancelFunc{},
		index:   map[string][]triggerRef{},
		defs:    map[string]workflow.Workflow{},
		queues:  map[string]*Queue{},
		dedups:  map[string]*Dedup{},
		clock:   time.Now,
	}

	release := make(chan struct{})
	var once sync.Once
	r.wg.Add(1)
	go func() {
		defer once.Do(r.wg.Done)
		<-release
	}()
	defer close(release)

	orig := StopTimeout
	StopTimeout = 50 * time.Millisecond
	defer func() { StopTimeout = orig }()

	start := time.Now()
	r.Stop()
	elapsed := time.Since(start)

	if elapsed < StopTimeout {
		t.Errorf("Stop() returned before timeout (%v); expected ~%v", elapsed, StopTimeout)
	}
	if elapsed > 5*StopTimeout {
		t.Errorf("Stop() hung past deadline (%v); expected ~%v", elapsed, StopTimeout)
	}
}
