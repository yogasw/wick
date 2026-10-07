//go:build linux || darwin || freebsd || netbsd || openbsd

package upgrade

import "github.com/cloudflare/tableflip"

func supported() bool { return true }

func newFlipper(pidFile string) (flipper, error) {
	// UpgradeTimeout bounds how long we wait for the child to report ready
	// before giving up and continuing to serve. Boot restore on a loaded host
	// is not instant, so this is well above tableflip's 1-minute default.
	upg, err := tableflip.New(tableflip.Options{
		UpgradeTimeout: upgradeTimeout,
		PIDFile:        pidFile,
	})
	if err != nil {
		return nil, err
	}
	return upg, nil
}
