package scriptpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValid(t *testing.T) {
	for _, p := range []string{"../x.ts", "a/../../x.ts", "/etc/x.ts", "~/x.ts"} {
		if Valid(p) == nil {
			t.Errorf("%q must be rejected", p)
		}
	}
	for _, p := range []string{"x.ts", "watches/new_rows.ts", "a/..b.ts"} {
		if err := Valid(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "return input.n + 1"
	if err := os.WriteFile(filepath.Join(dir, "scripts", "ok.ts"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.ts")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "scripts", "link.ts")); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "scripts", "big.ts")
	if err := os.WriteFile(big, make([]byte, MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Read(dir, "scripts/ok.ts")
	if err != nil || got != src {
		t.Fatalf("ok: %q %v", got, err)
	}
	// Read fresh: an edit shows up on the next read.
	if err := os.WriteFile(filepath.Join(dir, "scripts", "ok.ts"), []byte("return 2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read(dir, "scripts/ok.ts"); got != "return 2" {
		t.Fatalf("not re-read: %q", got)
	}

	cases := []struct {
		rel, want string
		denied    bool
	}{
		{"../secret.ts", "no ..", true},
		{outside, "relative", true},
		{"scripts/link.ts", "outside the project folder", true},
		{"scripts/gone.ts", "not found", false},
		{"scripts/sub", "not a regular file", false},
		{"scripts/big.ts", "too large", false},
	}
	for _, c := range cases {
		_, err := Read(dir, c.rel)
		if err == nil || !strings.Contains(err.Error(), c.want) || errors.Is(err, ErrDenied) != c.denied {
			t.Errorf("%s: err=%v denied=%v", c.rel, err, errors.Is(err, ErrDenied))
		}
	}
	if _, err := Read("", "scripts/ok.ts"); err == nil {
		t.Fatal("no folder must be refused")
	}
}
