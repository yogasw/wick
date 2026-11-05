package codemode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultRunnerKey is the runner wick downloads on its own when a run finds
// none installed.
const DefaultRunnerKey = "codemode_goja"

// EnvAutoInstall turns that download off when set to 0, false or off.
const EnvAutoInstall = "WICK_CODEMODE_AUTO_INSTALL"

const (
	// installTimeout bounds one download + verify + load.
	installTimeout = 5 * time.Minute
	// retryAfter keeps a failed install (offline host, catalog without the
	// runner) from being retried by every run; runs inside the window get
	// the same error straight away.
	retryAfter = time.Minute
)

// ErrAutoInstallOff means the runner download is turned off by EnvAutoInstall.
var ErrAutoInstallOff = errors.New("automatic runner download is off (" + EnvAutoInstall + ")")

// AutoInstallEnabled reports whether EnvAutoInstall allows the download.
func AutoInstallEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvAutoInstall))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// ManualInstallHint tells an admin how to install the runner by hand.
func ManualInstallHint() string {
	return "Install it by hand: Admin → Plugins → Sources, add the official plugins.json (or upload the " +
		DefaultRunnerKey + " zip from the wick GitHub releases) and install " + DefaultRunnerKey + "."
}

// Installer installs the default runner once for every run that needs it:
// the first caller starts the install, concurrent callers wait for the same
// result. The install itself is detached from the callers' contexts, so one
// caller giving up does not abort it for the rest.
type Installer struct {
	// Install downloads, verifies and loads the runner live. It returns once
	// the runner is registered with the service host.
	Install func(ctx context.Context) error
	// Enabled gates the download; nil means AutoInstallEnabled.
	Enabled func() bool

	mu       sync.Mutex
	cur      *flight
	lastErr  error
	failedAt time.Time
	now      func() time.Time
}

type flight struct {
	done chan struct{}
	err  error
}

func (i *Installer) clock() time.Time {
	if i.now != nil {
		return i.now()
	}
	return time.Now()
}

// Ensure installs the runner, or waits for the install already running.
func (i *Installer) Ensure(ctx context.Context) error {
	enabled := i.Enabled
	if enabled == nil {
		enabled = AutoInstallEnabled
	}
	if !enabled() {
		return fmt.Errorf("%w. %s", ErrAutoInstallOff, ManualInstallHint())
	}
	i.mu.Lock()
	if i.cur == nil && i.lastErr != nil && i.clock().Sub(i.failedAt) < retryAfter {
		err := i.lastErr
		i.mu.Unlock()
		return err
	}
	f := i.cur
	if f == nil {
		f = &flight{done: make(chan struct{})}
		i.cur = f
		go i.run(f)
	}
	i.mu.Unlock()
	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *Installer) run(f *flight) {
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	err := i.Install(ctx)
	if err != nil {
		err = fmt.Errorf("could not download the %s runner from the plugin catalog: %v. %s", DefaultRunnerKey, err, ManualInstallHint())
	}
	i.mu.Lock()
	i.cur = nil
	i.lastErr = err
	if err != nil {
		i.failedAt = i.clock()
	}
	i.mu.Unlock()
	f.err = err
	close(f.done)
}
