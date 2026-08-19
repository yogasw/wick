package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/agents/provider/memscope"
	"github.com/yogasw/wick/internal/agents/provider/memscope/wrapper"
	"github.com/yogasw/wick/pkg/safeexec"
)

// memory_wrapper.go installs the shim that puts each agent spawn in its
// own cgroup, for hosts where wick's built-in guard is not the thing
// doing it.
//
// Why a shim at all, when wick can wrap a spawn itself: the shim also
// catches spawns wick did not make. An operator running an agent CLI in
// a terminal, or another service invoking the same binary, goes through
// the same symlink. Wick's own guard only ever reaches wick's own
// children.
//
// Nothing here calls sudo. /usr/local/bin needs root, wick does not run
// as root, and a sudo call from a web request or a non-interactive
// service hangs on a password prompt nobody can answer. The privileged
// step is printed for a person to run, which also shows exactly what is
// about to change before it changes.

func memoryWrapperCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "wrapper",
		Short: "Install the per-agent cgroup shim, or report what it covers",
		Long: "Places each agent spawn in its own cgroup by putting a small shim in\n" +
			"front of the provider binary.\n\n" +
			"Unlike the built-in guard, this also catches spawns wick did not make —\n" +
			"a terminal session, or another service running the same binary — because\n" +
			"the interception is on the path, not on wick's own child processes.",
	}
	c.AddCommand(wrapperStatusCmd(), wrapperInstallCmd(), wrapperUninstallCmd())
	return c
}

// ---------------------------------------------------------------- status

func wrapperStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which agent processes are contained, and which are not",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			fmt.Fprintln(out, "Interception")
			for _, p := range wrapper.Detect(safeexec.LookPath, 0) {
				printLinkState(cmd, p)
			}

			procs, err := wrapper.Scan(wrapper.Providers, os.Getpid(), memscope.SliceName)
			if err != nil {
				fmt.Fprintf(out, "\nRunning processes\n  cannot inspect: %v\n", err)
				return nil
			}
			printProcesses(cmd, procs)
			return nil
		},
	}
}

// printProcesses reports every matching process, wick's and everyone
// else's.
//
// Reporting only wick's own would read as "all contained" while an
// identical binary runs unguarded under another service — same program,
// same memory, same machine, and the slice ceiling does not reach it.
// The operator asking why the box ran out of memory needs exactly the
// processes nobody installed a shim for.
func printProcesses(cmd *cobra.Command, procs []wrapper.ProcState) {
	out := cmd.OutOrStdout()
	s := wrapper.Summarize(procs)

	fmt.Fprintf(out, "\nRunning agent processes (%d)\n", s.Total)
	if s.Total == 0 {
		fmt.Fprintln(out, "  none")
		return
	}
	sort.Slice(procs, func(i, j int) bool {
		if procs[i].FromWick != procs[j].FromWick {
			return procs[i].FromWick
		}
		return procs[i].RSSBytes > procs[j].RSSBytes
	})
	for _, p := range procs {
		owner := "other"
		if p.FromWick {
			owner = "wick"
		}
		state := "NOT isolated"
		if p.Isolated {
			state = "isolated"
		}
		fmt.Fprintf(out, "  %-7s %-8s pid=%-7d %8s  %s\n",
			owner, p.Name, p.PID, humanBytes(p.RSSBytes), state)
	}

	fmt.Fprintf(out, "\n  %d isolated, %d with no ceiling at all\n", s.Isolated, s.Unisolated)

	// Listed individually rather than as a second count, because the fix
	// depends on which process it is: one of wick's own needs a setting
	// changed or the shim installed, while a stranger can only be bounded
	// at its own service — no shim reaches a caller using a full path.
	if s.Unisolated > 0 {
		fmt.Fprintln(out, "\n  Not isolated:")
		for _, p := range procs {
			if p.Isolated {
				continue
			}
			if p.FromWick {
				fmt.Fprintf(out, "    %s pid=%d — started by wick; turn on 'on spawn', or install the shim\n",
					p.Name, p.PID)
				continue
			}
			fmt.Fprintf(out, "    %s pid=%d — started outside wick\n", p.Name, p.PID)
		}
		fmt.Fprintln(out, "\n  A caller that runs the binary by its full path never passes through")
		fmt.Fprintln(out, "  the shim, so some of these can only be bounded at their own service:")
		fmt.Fprintln(out, "    sudo systemctl edit <that-service>   # MemoryMax=, MemoryHigh=infinity, MemorySwapMax=0")
	}
}

func printLinkState(cmd *cobra.Command, p wrapper.Provider) {
	out := cmd.OutOrStdout()
	target, err := filepath.EvalSymlinks(p.Link)
	switch {
	case err != nil:
		fmt.Fprintf(out, "  %-7s %s → (missing)\n", p.Name, p.Link)
	case wrapper.IsShim(target):
		fmt.Fprintf(out, "  %-7s %s → shim\n", p.Name, p.Link)
	default:
		fmt.Fprintf(out, "  %-7s %s → %s (not shimmed)\n", p.Name, p.Link, target)
	}
}

// --------------------------------------------------------------- install

func wrapperInstallCmd() *cobra.Command {
	var limitMB int
	c := &cobra.Command{
		Use:   "install [provider...]",
		Short: "Write the shim, and print the privileged step to finish it",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			targets := wrapper.Filter(wrapper.Detect(safeexec.LookPath, limitMB), args)
			if len(targets) == 0 {
				return fmt.Errorf("no matching provider found on PATH")
			}

			dir := wrapper.ShimDir()
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", dir, err)
			}

			fmt.Fprintln(out, "Shim written")
			var linkCmds []string
			stamp := time.Now().Format("20060102-150405")
			for _, p := range targets {
				path := filepath.Join(dir, p.Name)
				body := wrapper.RenderShim(p, memscope.SliceName)
				if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
					return fmt.Errorf("write %s: %w", path, err)
				}
				ceiling := "no ceiling"
				if p.LimitMB > 0 {
					ceiling = fmt.Sprintf("%d MB", p.LimitMB)
				}
				fmt.Fprintf(out, "  %-7s %s  (%s → %s)\n", p.Name, path, ceiling, p.RealBin)
				linkCmds = append(linkCmds, wrapper.LinkCommands(p, dir, stamp)...)
			}

			fmt.Fprintln(out, "\nRun these to point the path at it (needs root):")
			for _, c := range linkCmds {
				fmt.Fprintf(out, "  %s\n", c)
			}
			// Membership is fixed at spawn: a session already running
			// keeps its old placement until it ends. Saying so prevents
			// "I installed it and nothing changed".
			fmt.Fprintln(out, "\nSessions already running keep their current cgroup until they end.")
			fmt.Fprintln(out, "Start a new session, then: wick memory wrapper status")
			return nil
		},
	}
	c.Flags().IntVar(&limitMB, "limit-mb", 0,
		"per-session ceiling in MB; 0 creates the cgroup without a ceiling (the measure-mode shape)")
	return c
}

// ------------------------------------------------------------- uninstall

func wrapperUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall [provider...]",
		Short: "Print the step that restores the path, then remove the shim",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			targets := wrapper.Filter(wrapper.Detect(safeexec.LookPath, 0), args)
			if len(targets) == 0 {
				return fmt.Errorf("no matching provider found")
			}

			// Restore first, remove second. Reversed, there is a window
			// where the symlink points at a file that no longer exists
			// and every spawn fails — worse than what is being undone.
			fmt.Fprintln(out, "Run this FIRST, to point the path back at the real binary (needs root):")
			for _, p := range targets {
				for _, c := range wrapper.UnlinkCommands(p) {
					fmt.Fprintf(out, "  %s\n", c)
				}
			}

			fmt.Fprintln(out, "\nThen remove the shim:")
			for _, p := range targets {
				fmt.Fprintf(out, "  rm -f %s\n", filepath.Join(wrapper.ShimDir(), p.Name))
			}
			fmt.Fprintln(out, "\nRemoving the shim before restoring the path breaks every spawn in")
			fmt.Fprintln(out, "between, which is why this is not done for you in one step.")
			return nil
		},
	}
}
