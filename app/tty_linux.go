//go:build linux

package app

import (
	"os"

	"golang.org/x/sys/unix"
)

// isTerminal answers the only question that matters before prompting: is
// there a terminal on the other end, or a file / pipe / /dev/null? The
// termios ioctl is the real test — file mode bits cannot tell /dev/null
// apart from a tty, since both are character devices.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}
