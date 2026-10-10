//go:build !windows

package store

import (
	"os"
	"syscall"
)

// inflightLock is the flock a process holds on sessions/<id>/inflight.lock
// while a turn of that session is streaming into inflight.jsonl. It exists
// because a graceful upgrade leaves two wick processes alive at once: the
// successor's boot recovery must not fold a turn the draining predecessor is
// still running into conversation.jsonl as "interrupted".
//
// The lock lives on its own file, not on inflight.jsonl: that file is
// deleted and recreated every turn, and a lock on an unlinked inode guards
// nothing. flock is per open file description, so a second open in the SAME
// process conflicts too, and the kernel drops it when the holder dies — a
// crashed process never leaves a session looking busy.
type inflightLock struct{ f *os.File }

// tryLockInflight takes the session's turn lock without blocking. ok=false
// means another descriptor (normally another process) holds it, or the lock
// file could not be opened; callers treat both as "not ours".
func tryLockInflight(path string) (*inflightLock, bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return &inflightLock{f: f}, true
}

// release drops the lock. Safe on nil and idempotent.
func (l *inflightLock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}
