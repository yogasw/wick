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

// HasSubscribers is what the approval gate uses to decide whether a
// prompt still has somebody who can answer it, so a wrong answer here
// either blocks a watched command or waits on an empty room.
func TestBroadcaster_HasSubscribers(t *testing.T) {
	b := NewBroadcaster()

	if b.HasSubscribers("S1") {
		t.Error("reported a viewer with nobody subscribed")
	}

	_, unsub := b.Subscribe("S1")
	if !b.HasSubscribers("S1") {
		t.Error("missed a subscriber on the session's own stream")
	}
	if b.HasSubscribers("S2") {
		t.Error("another session's subscriber leaked into S2")
	}

	unsub()
	if b.HasSubscribers("S1") {
		t.Error("still reporting a viewer after unsubscribe")
	}
}

// The sidebar stream subscribes globally (""), and it renders approval
// prompts too — so a user sitting on the session list can still answer.
func TestBroadcaster_HasSubscribers_GlobalCounts(t *testing.T) {
	b := NewBroadcaster()
	_, unsub := b.Subscribe("")
	defer unsub()

	if !b.HasSubscribers("S1") {
		t.Error("global subscriber should count as a viewer for any session")
	}
}
