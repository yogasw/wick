//go:build !(linux || darwin || freebsd || netbsd || openbsd)

package upgrade

func supported() bool { return false }

// newFlipper never succeeds off unix: handing a listening socket to another
// process needs descriptor passing, which Windows does not offer. Callers get
// a plain listener and the classic stop/start restart.
func newFlipper(string) (flipper, error) { return nil, ErrUnsupported }
