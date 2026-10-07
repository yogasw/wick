package skillsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardTestWrite_RefusesOutsideTemp(t *testing.T) {
	if err := guardTestWrite([]string{filepath.Join(t.TempDir(), "skills")}); err != nil {
		t.Fatalf("temp dir refused: %v", err)
	}
	err := guardTestWrite([]string{"/nonexistent-wick-home/.claude/skills"})
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("want refusal outside temp, got %v", err)
	}
}

// TestSyncBuiltin_NeverWritesOutsideTemp points HOME somewhere outside the temp
// dir and checks SyncBuiltin refuses instead of creating anything there.
func TestSyncBuiltin_NeverWritesOutsideTemp(t *testing.T) {
	home := "/nonexistent-wick-home"
	setTestHome(t, home)
	t.Setenv("WICK_DATA_DIR", "")
	if _, err := SyncBuiltin(); err == nil {
		t.Fatal("SyncBuiltin wrote outside temp under go test")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("%s was created: %v", home, err)
	}
}

func TestUnderDir(t *testing.T) {
	cases := []struct {
		p, root string
		want    bool
	}{
		{"/tmp/x/y", "/tmp", true},
		{"/tmp", "/tmp", true},
		{"/tmpx/y", "/tmp", false},
		{"/home/u/.claude/skills", "/tmp", false},
	}
	for _, c := range cases {
		if got := underDir(c.p, c.root); got != c.want {
			t.Errorf("underDir(%q,%q)=%v want %v", c.p, c.root, got, c.want)
		}
	}
}
