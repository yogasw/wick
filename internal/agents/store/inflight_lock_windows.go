//go:build windows

package store

// inflightLock is a no-op on Windows: without descriptor passing there is
// never a second wick process draining a turn (see upgrade/intake_windows.go),
// so every leftover inflight.jsonl belongs to a dead process.
type inflightLock struct{}

// tryLockInflight always succeeds: nothing else can be running the turn.
func tryLockInflight(string) (*inflightLock, bool) { return &inflightLock{}, true }

// release does nothing.
func (l *inflightLock) release() {}
