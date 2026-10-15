//go:build linux || darwin || freebsd || netbsd || openbsd

package upgrade

import "github.com/cloudflare/tableflip"

func supported() bool { return true }

func newFlipper(pidFile string) (flipper, error) {
	// UpgradeTimeout bounds how long we wait for the child to report ready
	// before giving up and continuing to serve. Unbounded unless configured —
	// see upgradeTimeout.
	timeout := upgradeTimeout()
	if timeout <= 0 {
		timeout = noHandoverLimit
	}
	upg, err := tableflip.New(tableflip.Options{
		UpgradeTimeout: timeout,
		PIDFile:        pidFile,
	})
	if err != nil {
		return nil, err
	}
	return upg, nil
}
