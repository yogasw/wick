package claude

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

// eventCollector buckets parser events for assertions. Concurrency-safe.
type eventCollector struct {
	mu     sync.Mutex
	events []event.AgentEvent
	// turnText accumulates TextDelta per turn — flushed into the slice
	// on each Done event so we can assert per-turn text bodies.
	current      strings.Builder
	turnSnapshot []string
}

func (c *eventCollector) add(ev event.AgentEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)

	switch ev.Type {
	case event.TextDelta:
		c.current.WriteString(ev.Text)
	case event.Done, event.Error:
		c.turnSnapshot = append(c.turnSnapshot, c.current.String())
		c.current.Reset()
	}
}

func (c *eventCollector) doneCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if e.Type == event.Done {
			n++
		}
	}
	return n
}

func (c *eventCollector) sessionStartCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if e.Type == event.SessionStart {
			n++
		}
	}
	return n
}

func (c *eventCollector) textPerTurn() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.turnSnapshot))
	copy(out, c.turnSnapshot)
	return out
}

// waitFor polls cond until true or timeout. Used instead of a fixed
// sleep so claude latency variance doesn't cause flakes.
func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("waitFor: condition never satisfied within %v", timeout)
}

func getenv(k string) string { return os.Getenv(k) }

func containsCI(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
