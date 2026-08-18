package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/agents/provider/memscope"
)

// agentexec_cmd.go wires the hidden `wick __agent-exec` subcommand that
// memscope.WrapArgvCgroupFS re-execs wick through on a systemd-less host.
// It is the cgroupfs backend's stand-in for what systemd-run performs for
// the systemd backend — "join a cgroup, then become the real command" —
// done by wick's own binary because nothing else on a host like a Fly.io
// Machine is available to do it.
//
// Not meant to be typed by a person: no Short/Long help text, Hidden so
// it never appears in `wick --help` or shell completion. The double
// underscore prefix (matching AgentExecSubcommand) is a second signal of
// the same thing on the rare path where someone greps a process list.
func agentExecCmd() *cobra.Command {
	var root, slice, unit string
	var limitMB int

	c := &cobra.Command{
		Use:    memscope.AgentExecSubcommand + " -- <bin> [args...]",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if root == "" || slice == "" || unit == "" {
				return fmt.Errorf("%s: --root, --slice, and --unit are required", memscope.AgentExecSubcommand)
			}
			// RunAgentExec replaces this process on success — nothing
			// after it runs. On failure, the caller (memguard's Wrap
			// chose to re-exec through this path in the first place) sees
			// a nonzero exit and the wrapped spawn simply fails, exactly
			// as it would if the real agent binary itself failed to
			// start. There is no "run unguarded" fallback from inside
			// this process: that decision already happened one level up,
			// in memguard.wrapCgroupFS, before this subcommand was ever
			// invoked.
			return memscope.RunAgentExec(memscope.ExecOpts{
				Root: root, Slice: slice, Unit: unit, LimitMB: limitMB,
				Bin: args[0], Args: args[1:],
			})
		},
	}
	c.Flags().StringVar(&root, "root", "", "cgroup v1 memory-controller mount root")
	c.Flags().StringVar(&slice, "slice", "", "cgroup slice (directory) name")
	c.Flags().StringVar(&unit, "unit", "", "scope name, without the .scope suffix")
	c.Flags().IntVar(&limitMB, "limit-mb", 0, "memory ceiling in MB; 0 = join the group, apply no ceiling")
	return c
}
