package pool

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
)

// Releasing a slot keeps a buffer that still holds messages: they are
// drained from memory, even after meta.json's PendingInput was rewritten
// by another meta writer. An empty one is dropped, and a message a late
// holder adds to it reaches the session's live buffer.
func TestReleaseSlotKeepsHeldMessages(t *testing.T) {
	p, layout, _, _ := failoverEnv(t, false, nil, false)
	key := sessionKey("S", "default")
	occupy := func() {
		p.mu.Lock()
		p.active[key] = &runEntry{sessID: "S", agentNm: "default"}
		p.mu.Unlock()
	}
	clobberDisk := func() {
		t.Helper()
		sess, err := session.Load(layout, "S")
		if err != nil {
			t.Fatal(err)
		}
		sess.Meta.PendingInput = nil
		if err := session.SaveMeta(layout, "S", sess.Meta); err != nil {
			t.Fatal(err)
		}
	}

	buf, err := p.bufferFor("S")
	if err != nil {
		t.Fatal(err)
	}
	if err := buf.Append("held"); err != nil {
		t.Fatal(err)
	}
	occupy()
	p.releaseSlot(key)
	clobberDisk()
	got, err := p.bufferFor("S")
	if err != nil {
		t.Fatal(err)
	}
	if got != buf || len(got.Lines()) != 1 || got.Lines()[0] != "held" {
		t.Fatalf("after release: buffer %p lines %q, want the held message", got, got.Lines())
	}

	// Empty: dropped; a late append on the old object is not lost.
	if _, err := buf.Drain(); err != nil {
		t.Fatal(err)
	}
	occupy()
	p.releaseSlot(key)
	p.mu.Lock()
	_, kept := p.buffers["S"]
	p.mu.Unlock()
	if kept {
		t.Fatal("empty buffer kept after release")
	}
	if err := buf.Append("late"); err != nil {
		t.Fatal(err)
	}
	clobberDisk()
	got, err = p.bufferFor("S")
	if err != nil {
		t.Fatal(err)
	}
	if l := got.Lines(); len(l) != 1 || l[0] != "late" {
		t.Fatalf("late append lost: %q", l)
	}

	// A late append while a new buffer is already live goes to that one.
	if _, err := got.Drain(); err != nil {
		t.Fatal(err)
	}
	occupy()
	p.releaseSlot(key)
	fresh, err := p.bufferFor("S")
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Append("later"); err != nil {
		t.Fatal(err)
	}
	if l := fresh.Lines(); len(l) != 1 || l[0] != "later" {
		t.Fatalf("live buffer = %q, want the late message", l)
	}
}
