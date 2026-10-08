//go:build windows

package schedule

import (
	"context"
	"os/exec"
)

// limitedBash runs script without limits off unix.
func limitedBash(ctx context.Context, script string, timeoutSec int) *exec.Cmd {
	return exec.CommandContext(ctx, "bash", "-c", script)
}

// setProcessGroup is a no-op off unix: the default cancel kills the script.
func setProcessGroup(cmd *exec.Cmd) {}
