//go:build windows

package schedule

import (
	"context"
	"os/exec"

	"github.com/yogasw/wick/internal/pkg/envscrub"
	"github.com/yogasw/wick/pkg/safeexec"
)

// limitedBash runs script without limits off unix.
func limitedBash(ctx context.Context, script string, timeoutSec int) *exec.Cmd {
	cmd := safeexec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = envscrub.ScrubOSEnv()
	return cmd
}

// setProcessGroup is a no-op off unix: the default cancel kills the script.
func setProcessGroup(cmd *exec.Cmd) {}
