package router9

import (
	"testing"
	"time"
)

func TestLogBufferSnapshotAndTrim(t *testing.T) {
	l := newLogBuffer()
	_, _ = l.Write([]byte("hello\n"))
	if l.Snapshot() != "hello\n" {
		t.Fatalf("snapshot = %q", l.Snapshot())
	}
	l.Reset()
	if l.Snapshot() != "" {
		t.Errorf("snapshot after reset = %q, want empty", l.Snapshot())
	}
}

func TestLogBufferSubscribeGetsSnapshotAndLiveChunks(t *testing.T) {
	l := newLogBuffer()
	_, _ = l.Write([]byte("existing\n"))

	initial, ch, unsub := l.subscribe()
	defer unsub()
	if initial != "existing\n" {
		t.Fatalf("initial snapshot = %q", initial)
	}

	_, _ = l.Write([]byte("new line\n"))
	select {
	case c := <-ch:
		if c != "new line\n" {
			t.Errorf("live chunk = %q", c)
		}
	case <-time.After(time.Second):
		t.Fatal("no live chunk delivered to subscriber")
	}
}

func TestLogBufferResetNotifiesSubscribers(t *testing.T) {
	l := newLogBuffer()
	_, ch, unsub := l.subscribe()
	defer unsub()

	l.Reset()
	select {
	case c := <-ch:
		if c != logResetSentinel {
			t.Errorf("expected reset sentinel, got %q", c)
		}
	case <-time.After(time.Second):
		t.Fatal("reset sentinel not delivered")
	}
}

func TestLogBufferUnsubscribeStopsDelivery(t *testing.T) {
	l := newLogBuffer()
	_, ch, unsub := l.subscribe()
	unsub()
	// Channel is closed after unsubscribe.
	if _, open := <-ch; open {
		t.Error("channel should be closed after unsubscribe")
	}
	// A write after unsubscribe must not panic (no send on closed channel).
	_, _ = l.Write([]byte("after\n"))
}

func TestLogBufferSlowSubscriberDoesNotBlockWrite(t *testing.T) {
	l := newLogBuffer()
	_, _, unsub := l.subscribe() // never drained
	defer unsub()
	done := make(chan struct{})
	go func() {
		for i := 0; i < logSubBuffer*3; i++ {
			_, _ = l.Write([]byte("x\n"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on a slow log subscriber")
	}
}
