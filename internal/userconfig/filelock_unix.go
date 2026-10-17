//go:build !windows

package userconfig

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// lockWait is how long lockFile keeps trying, lockPause the pause
// between tries.
const (
	lockWait  = 10 * time.Second
	lockPause = 25 * time.Millisecond
)

// lockFile takes an exclusive flock on path. While another process holds
// it, it pauses and tries again, up to lockWait. The kernel drops the
// lock if the holder dies, so a crash never leaves it stuck.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if err != syscall.EWOULDBLOCK || time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		time.Sleep(lockPause)
	}
}
