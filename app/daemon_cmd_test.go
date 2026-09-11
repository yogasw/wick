package app

import (
	"os"
	"strings"
	"testing"
)

// A prompt that reads EOF as "yes" is how an unattended script deploys a
// binary nobody approved, so every non-answer is checked explicitly.
func TestPromptYesNo(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"enter means yes", "\n", true},
		{"y", "y\n", true},
		{"yes, padded and capitalised", "  YES  \n", true},
		{"n", "n\n", false},
		{"no", "no\n", false},
		{"anything else is not consent", "maybe\n", false},
		{"EOF is not consent", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := promptYesNo(strings.NewReader(tc.input), "swap?"); got != tc.want {
				t.Fatalf("promptYesNo(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// /dev/null is a character device, so the file-mode heuristic calls it a
// terminal. That is precisely the shape a deploy script has.
func TestIsTerminalRejectsDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("/dev/null must not be reported as a terminal")
	}
}

func TestIsTerminalRejectsFileAndNil(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-tty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("a regular file must not be reported as a terminal")
	}
	if isTerminal(nil) {
		t.Error("nil must not be reported as a terminal")
	}
}
