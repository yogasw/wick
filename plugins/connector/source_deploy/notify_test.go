package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFlushNotificationsDeliversPendingOnce(t *testing.T) {
	dir := t.TempDir()
	s := &Service{dir: dir}
	runs := s.runsDir()
	if err := os.MkdirAll(runs, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string, age time.Duration) {
		p := filepath.Join(runs, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		_ = os.Chtimes(p, at, at)
	}
	write("ok.exit", "0\n", time.Minute)            // pending, succeeded
	write("bad.exit", "3\n", time.Minute)           // pending, failed
	write("done.exit", "0\n", time.Minute)          // already delivered
	write("done.notified", "", time.Minute)         //
	write("old.exit", "0\n", 2*notifyPendingWindow) // too old to announce now

	var mu sync.Mutex
	got := map[string]string{}
	s.publish = func(id, exitFile, state string, code int) error {
		mu.Lock()
		got[id] = state
		mu.Unlock()
		return os.WriteFile(notifiedPath(exitFile), nil, 0o600)
	}
	s.FlushNotifications()
	if len(got) != 2 || got["ok"] != StateSucceeded || got["bad"] != StateFailed {
		t.Fatalf("published %v", got)
	}
	got = map[string]string{}
	s.FlushNotifications()
	if len(got) != 0 {
		t.Fatalf("published twice: %v", got)
	}
}
