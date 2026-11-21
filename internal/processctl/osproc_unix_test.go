//go:build !windows

package processctl

import (
	"github.com/yogasw/wick/pkg/safeexec"
	"os"
	"path/filepath"
	"testing"
)

func TestQueryProcessSurvivesReplacedBinary(t *testing.T) {
	src, err := safeexec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Skip(err)
	}
	bin := filepath.Join(t.TempDir(), "daemon")
	if err := os.WriteFile(bin, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := safeexec.Command(bin, "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	// An update installs a new file at the same path; the old inode is gone.
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, b, 0o755); err != nil {
		t.Fatal(err)
	}
	info := QueryProcess(cmd.Process.Pid)
	if info.Exe == "" {
		t.Skip("/proc/<pid>/exe not readable here")
	}
	if !info.Verified(bin) {
		t.Fatalf("running process not recognised after its binary was replaced: exe=%q", info.Exe)
	}
}
