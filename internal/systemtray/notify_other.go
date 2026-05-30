//go:build !windows && !headless

package systemtray

import (
	"runtime"

	"github.com/yogasw/wick/internal/safeexec"
)

// notify shows an OS-level notification.
// macOS: osascript display notification. Linux: notify-send (best-effort,
// silently no-op if the binary is missing on a headless box).
func notify(title, message string) error {
	if runtime.GOOS == "darwin" {
		script := `display notification "` + escapeAS(message) + `" with title "` + escapeAS(title) + `"`
		return safeexec.Command("osascript", "-e", script).Start()
	}
	return safeexec.Command("notify-send", title, message).Start()
}

func escapeAS(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '"' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
