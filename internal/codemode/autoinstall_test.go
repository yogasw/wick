package codemode

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunInstallsMissingRunner: with no runner installed the run calls
// Install, then picks the runner it loaded on the same run.
func TestRunInstallsMissingRunner(t *testing.T) {
	h := hostFor(t, &fakeRunner{})
	loaded := h.Runners
	var installed atomic.Bool
	h.Runners = func() []Runner {
		if !installed.Load() {
			return nil
		}
		return loaded()
	}
	inst := &Installer{Enabled: func() bool { return true }, Install: func(context.Context) error {
		installed.Store(true)
		return nil
	}}
	h.Install = inst.Ensure
	res, err := h.Run(context.Background(), Request{Code: "CALL conn:a/list"})
	if err != nil || !res.OK || res.Runner != "codemode_fake" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

// TestInstallOnlyWhenNoRunner: a runner that lacks the language is not a
// reason to download another one.
func TestInstallOnlyWhenNoRunner(t *testing.T) {
	h := hostFor(t, &fakeRunner{})
	h.Install = func(context.Context) error { t.Fatal("install called with a runner present"); return nil }
	if _, err := h.Run(context.Background(), Request{Lang: "py", Code: "1"}); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("err = %v", err)
	}
}

// TestInstallerSingleFlight: concurrent runs share one install.
func TestInstallerSingleFlight(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	inst := &Installer{Enabled: func() bool { return true }, Install: func(context.Context) error {
		calls.Add(1)
		<-release
		return nil
	}}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = inst.Ensure(context.Background())
		}(i)
	}
	// Let every caller join the flight before it lands.
	for {
		inst.mu.Lock()
		started := inst.cur != nil
		inst.mu.Unlock()
		if started {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("install ran %d times, want 1", n)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
}

// TestInstallerFailureIsCachedThenRetried: an offline host fails once with
// the manual-install hint, later runs get the same error without another
// download until the retry window passes.
func TestInstallerFailureIsCachedThenRetried(t *testing.T) {
	now := time.Unix(1000, 0)
	var calls atomic.Int32
	inst := &Installer{Enabled: func() bool { return true }, now: func() time.Time { return now },
		Install: func(context.Context) error {
			calls.Add(1)
			return errors.New("dial tcp: network is unreachable")
		}}
	err := inst.Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "network is unreachable") || !strings.Contains(err.Error(), "Install it by hand") {
		t.Fatalf("err = %v", err)
	}
	if err2 := inst.Ensure(context.Background()); err2 == nil || calls.Load() != 1 {
		t.Fatalf("retried inside the window: calls=%d err=%v", calls.Load(), err2)
	}
	now = now.Add(retryAfter + time.Second)
	_ = inst.Ensure(context.Background())
	if calls.Load() != 2 {
		t.Fatalf("not retried after the window: calls=%d", calls.Load())
	}
}

// TestInstallerOff: the env switch stops the download and says how to
// install by hand.
func TestInstallerOff(t *testing.T) {
	t.Setenv(EnvAutoInstall, "0")
	inst := &Installer{Install: func(context.Context) error { t.Fatal("download while off"); return nil }}
	err := inst.Ensure(context.Background())
	if !errors.Is(err, ErrAutoInstallOff) || !strings.Contains(err.Error(), DefaultRunnerKey) {
		t.Fatalf("err = %v", err)
	}
	h := Host{Runners: func() []Runner { return nil }, Install: inst.Ensure}
	if _, err := h.Run(context.Background(), Request{Code: "1"}); !errors.Is(err, ErrNoRunner) || !strings.Contains(err.Error(), EnvAutoInstall) {
		t.Fatalf("run err = %v", err)
	}
}

// TestInstallerCallerCancel: a caller giving up does not abort the install
// for the others.
func TestInstallerCallerCancel(t *testing.T) {
	release := make(chan struct{})
	inst := &Installer{Enabled: func() bool { return true }, Install: func(ctx context.Context) error {
		<-release
		return ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- inst.Ensure(ctx) }()
	other := make(chan error, 1)
	go func() { other <- inst.Ensure(context.Background()) }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller: %v", err)
	}
	close(release)
	if err := <-other; err != nil {
		t.Fatalf("other caller: %v", err)
	}
}
