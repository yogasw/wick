package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

func TestBroadcasterUnsubRemovesStaleKey(t *testing.T) {
	b := NewBroadcaster()

	_, unsub := b.Subscribe("session-1")
	_, unsub2 := b.Subscribe("session-1")

	b.mu.RLock()
	if _, ok := b.subs["session-1"]; !ok {
		t.Fatal("key should exist after subscribe")
	}
	b.mu.RUnlock()

	unsub()

	b.mu.RLock()
	if _, ok := b.subs["session-1"]; !ok {
		t.Fatal("key should still exist: one subscriber remains")
	}
	b.mu.RUnlock()

	unsub2()

	b.mu.RLock()
	_, keyExists := b.subs["session-1"]
	b.mu.RUnlock()

	if keyExists {
		t.Fatal("stale key still present after all subscribers removed")
	}
}

func TestBroadcasterPublishAfterUnsub(t *testing.T) {
	b := NewBroadcaster()
	ch, unsub := b.Subscribe("sess")

	unsub()

	b.Publish("sess", "agent", event.AgentEvent{Type: event.TextDelta, Text: "hi"})

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed")
		}
	default:
	}
}
