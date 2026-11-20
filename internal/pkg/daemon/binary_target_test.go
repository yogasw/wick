package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A concurrent manual start writes a new live PID before the requested
// tableflip child is ready. Waiting on that PID alone used to report a
// successful deploy even when no handover had completed.
func TestWaitSuccessorRejectsLivePIDWithoutHandoverReceipt(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Dir: dir, PIDFile: filepath.Join(dir, "run.pid")}
	if err := os.WriteFile(p.PIDFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := WaitSuccessor(p, "wick-agent", os.Getpid()+1, time.Millisecond)
	if !errors.Is(err, ErrReloadTimeout) {
		t.Fatalf("WaitSuccessor error = %v, want %v", err, ErrReloadTimeout)
	}
}
