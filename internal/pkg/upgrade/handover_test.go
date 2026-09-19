package upgrade

import (
	"net"
	"sync"
	"testing"
	"time"
)

// stubFlipper stands in for tableflip: Upgrade blocks until released, which
// is the behaviour that matters here — a real handover spends ~80s inside
// that call while this process keeps serving.
type stubFlipper struct {
	started  chan struct{}
	release  chan struct{}
	err      error
	stopOnce sync.Once
}

func (f *stubFlipper) Listen(string, string) (net.Listener, error) { return nil, nil }
func (f *stubFlipper) Ready() error                                { return nil }
func (f *stubFlipper) Exit() <-chan struct{}                       { return nil }
func (f *stubFlipper) Stop()                                       { f.stopOnce.Do(func() {}) }
func (f *stubFlipper) HasParent() bool                             { return false }
func (f *stubFlipper) Upgrade() error {
	close(f.started)
	<-f.release
	return f.err
}

// The window this flag exists for is the one nothing else records: a
// successor is booting and THIS process is still answering, so a page asking
// "is something happening" gets a truthful yes instead of noticing minutes
// later that the version it was drawn with is gone.
func TestHandingOverSpansTheSuccessorBoot(t *testing.T) {
	if HandingOver() {
		t.Fatal("a process that has not been asked to hand over must not claim it is")
	}
	f := &stubFlipper{started: make(chan struct{}), release: make(chan struct{})}
	u := &Upgrader{flip: f, never: make(chan struct{})}

	done := make(chan error, 1)
	go func() { done <- u.Upgrade() }()

	select {
	case <-f.started:
	case <-time.After(2 * time.Second):
		t.Fatal("Upgrade never reached the flipper")
	}
	if !HandingOver() {
		t.Error("a successor is booting and the process says nothing is happening")
	}

	close(f.release)
	if err := <-done; err != nil {
		t.Fatalf("Upgrade = %v, want nil", err)
	}
	// And it has to go back down, or the row it drives sticks on forever.
	if HandingOver() {
		t.Error("the handover finished but the process still reports one running")
	}
}

// A successor that never starts leaves the process serving exactly as before.
// The flag has to come back down there too — a failed handover that reads as
// a permanent "reloading" is worse than no indicator at all.
func TestHandingOverClearsWhenTheSuccessorFails(t *testing.T) {
	f := &stubFlipper{started: make(chan struct{}), release: make(chan struct{}), err: ErrUnsupported}
	close(f.release)
	u := &Upgrader{flip: f, never: make(chan struct{})}
	if err := u.Upgrade(); err == nil {
		t.Fatal("Upgrade = nil, want the flipper's error")
	}
	if HandingOver() {
		t.Error("the spawn failed but the process still reports a handover running")
	}
}

// An upgrader that cannot hand off at all never claims to be mid-handover.
func TestHandingOverStaysFalseWhenUpgradeIsUnsupported(t *testing.T) {
	var u *Upgrader
	if err := u.Upgrade(); err != ErrUnsupported {
		t.Fatalf("Upgrade = %v, want ErrUnsupported", err)
	}
	if HandingOver() {
		t.Error("a build with no graceful upgrade reported a handover")
	}
}
