//go:build !linux

package app

import "os"

// isTerminal falls back to the character-device heuristic off Linux. It is
// weaker — /dev/null passes it — which is why promptYesNo refuses on EOF
// rather than relying on this alone.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
