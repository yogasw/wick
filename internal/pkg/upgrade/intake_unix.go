//go:build !windows

package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// IntakeBaton is the right to accept NEW work: channel listeners (Slack
// socket mode, Telegram polling), the workflow cron/router, and the
// scheduled-message runner. Exactly one process may hold it.
//
// It exists because a graceful upgrade means two wick processes are briefly
// alive, and those subsystems are singletons in a way HTTP is not. Two Slack
// socket-mode connections split inbound events between the old and the new
// binary at random; two cron loops fire every job twice; two schedule runners
// deliver every scheduled message twice. None of that shows up in a test that
// only checks the port stayed open — it shows up in production as duplicate
// work, which is worse than a short restart.
//
// The lock is an flock, so the kernel releases it even if the holder is
// SIGKILLed: a crashed process never wedges the baton.
type IntakeBaton struct {
	f    *os.File
	once sync.Once
}

func acquireIntake(ctx context.Context, baseDir string) (*IntakeBaton, error) {
	if baseDir == "" {
		return nil, fmt.Errorf("intake: empty base dir")
	}
	path := filepath.Join(baseDir, "intake.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("intake: open %s: %w", path, err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			// Who holds it, for humans reading the data dir. The flock is the
			// actual authority; this file content is only a hint.
			_ = f.Truncate(0)
			_, _ = f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
			return &IntakeBaton{f: f}, nil
		}
		if !isWouldBlock(err) {
			f.Close()
			return nil, fmt.Errorf("intake: flock: %w", err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func isWouldBlock(err error) bool {
	return err == syscall.EWOULDBLOCK || err == syscall.EAGAIN
}

// Release hands the baton to whoever is waiting. Idempotent.
func (b *IntakeBaton) Release() {
	if b == nil || b.f == nil {
		return
	}
	b.once.Do(func() {
		_ = syscall.Flock(int(b.f.Fd()), syscall.LOCK_UN)
		_ = b.f.Close()
	})
}
