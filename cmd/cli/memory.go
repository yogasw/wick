package cli

import (
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/pkg/memreport"
	"github.com/yogasw/wick/internal/pkg/sysmem"
)

// memory.go is the calibration tool. It reads /proc directly, so it works
// with the memory guard switched off, on a machine that has never been
// configured, and on Termux where scope isolation is unavailable.
//
// That is the point: enabling a limit you guessed is how agents start
// dying for reasons the operator cannot explain. This answers "what do
// these agents actually use" before anything is turned on.

// agentBinaries are the process names worth reporting as roots.
var agentBinaries = []string{"claude", "codex", "gemini", "node", "python3"}

func memoryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "memory",
		Short: "Inspect agent memory usage",
	}
	c.AddCommand(memoryReportCmd(), memoryWrapperCmd())
	return c
}

func memoryReportCmd() *cobra.Command {
	var watch time.Duration
	var forDur time.Duration

	c := &cobra.Command{
		Use:   "report",
		Short: "Show what each agent is using, and suggest limits for this machine",
		Long: "Reads /proc and reports memory per agent process tree — including " +
			"the browsers and tools an agent starts, which is where the memory " +
			"usually is. Works with the memory guard turned off.\n\n" +
			"A single snapshot almost certainly misses a browser's peak, so use " +
			"--watch to sample over time before choosing a limit.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if watch > 0 {
				return runMemoryWatch(cmd, watch, forDur)
			}
			return runMemoryReport(cmd)
		},
	}
	c.Flags().DurationVar(&watch, "watch", 0, "Sample repeatedly at this interval (e.g. 30s) and report the peak seen")
	c.Flags().DurationVar(&forDur, "for", time.Hour, "How long to keep sampling with --watch")
	return c
}

// runMemoryReport prints one snapshot.
func runMemoryReport(cmd *cobra.Command) error {
	procs, err := memreport.Snapshot()
	if err != nil {
		// Not a failure: say why it cannot help and exit cleanly. An
		// operator running this on their laptop should not get an error.
		fmt.Fprintln(cmd.OutOrStdout(), err)
		return nil
	}
	printMachine(cmd)
	roots := memreport.Roots(procs, agentBinaries)
	peaks := map[int]uint64{}
	for _, r := range roots {
		peaks[r.PID] = memreport.SumSubtree(procs, r.PID)
	}
	printRoots(cmd, procs, roots, peaks)
	printSuggestions(cmd, peaks)
	return nil
}

// runMemoryWatch samples on an interval and reports the PEAK per root.
//
// The peak is what a limit must accommodate, and it is exactly what a
// single snapshot misses — a browser can allocate and release a gigabyte
// between two samples.
func runMemoryWatch(cmd *cobra.Command, every, total time.Duration) error {
	if _, err := memreport.Snapshot(); err != nil {
		fmt.Fprintln(cmd.OutOrStdout(), err)
		return nil
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Sampling every %s for %s. Press Ctrl-C to stop early.\n\n", every, total)

	peaks := map[int]uint64{}
	names := map[int]string{}

	deadline := time.After(total)
	tick := time.NewTicker(every)
	defer tick.Stop()

	sample := func() {
		procs, err := memreport.Snapshot()
		if err != nil {
			return
		}
		for _, r := range memreport.Roots(procs, agentBinaries) {
			names[r.PID] = r.Name
			if got := memreport.SumSubtree(procs, r.PID); got > peaks[r.PID] {
				peaks[r.PID] = got
			}
		}
	}
	sample() // don't wait a full interval for the first reading

	for done := false; !done; {
		select {
		case <-tick.C:
			sample()
		case <-deadline:
			done = true
		}
	}

	printMachine(cmd)
	fmt.Fprintf(out, "\nPeak per agent over %s\n", total)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, pid := range sortedPIDs(peaks) {
		fmt.Fprintf(w, "  %s\tpid %d\tpeak %s\n", names[pid], pid, humanBytes(peaks[pid]))
	}
	_ = w.Flush()
	printSuggestions(cmd, peaks)
	return nil
}

func printMachine(cmd *cobra.Command) {
	out := cmd.OutOrStdout()
	total, okT := sysmem.Total()
	avail, okA := sysmem.Available()
	switch {
	case okT && okA:
		fmt.Fprintf(out, "Machine: %s total, %s available\n", humanBytes(total), humanBytes(avail))
	case okT:
		fmt.Fprintf(out, "Machine: %s total\n", humanBytes(total))
	default:
		fmt.Fprintln(out, "Machine: memory size unknown on this platform")
	}
}

func printRoots(cmd *cobra.Command, procs, roots []memreport.Proc, peaks map[int]uint64) {
	out := cmd.OutOrStdout()
	if len(roots) == 0 {
		fmt.Fprintln(out, "\nNo agent processes running.")
		return
	}
	fmt.Fprintf(out, "\nAgent process trees (%d)\n", len(roots))
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, r := range roots {
		line := fmt.Sprintf("  %s\tpid %d\ttree %s", r.Name, r.PID, humanBytes(peaks[r.PID]))
		// Name the heaviest descendant: the total alone invites raising a
		// limit, while "of which chromium is 1.2 GB" names the cause.
		if big := memreport.LargestDescendant(procs, r.PID); big.PID != 0 {
			line += fmt.Sprintf("\t<- %s %s", big.Name, humanBytes(big.RSSBytes))
		}
		fmt.Fprintln(w, line)
	}
	_ = w.Flush()
}

// printSuggestions turns observed peaks into settings, with headroom.
//
// Headroom is not padding for its own sake: a limit set exactly at the
// observed peak kills the next run that does slightly more work, which is
// the failure mode that makes operators distrust the guard and turn it off.
func printSuggestions(cmd *cobra.Command, peaks map[int]uint64) {
	out := cmd.OutOrStdout()
	total, ok := sysmem.Total()
	if !ok {
		return
	}
	d := agentconfig.DeriveMemoryDefaults(total, 1)

	var worst uint64
	for _, v := range peaks {
		if v > worst {
			worst = v
		}
	}

	fmt.Fprintln(out, "\nSuggested settings for this machine")
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  max_concurrent\t1\n")
	if worst > 0 {
		// Shared with the Resources page so both suggest the same number.
		fmt.Fprintf(w, "  agent_memory_max_mb\t%d\t(peak seen %s + 30%%)\n",
			agentconfig.SuggestLimitMB(worst), humanBytes(worst))
	} else {
		fmt.Fprintf(w, "  agent_memory_max_mb\t%d\t(no agents seen; derived from RAM)\n", d.AgentMaxMB)
	}
	fmt.Fprintf(w, "  agents_total_memory_mb\t%d\n", d.AgentsTotalMB)
	fmt.Fprintf(w, "  tool_memory_max_mb\t%d\n", d.ToolMaxMB)
	fmt.Fprintf(w, "  min_free_memory_mb\t%d\n", d.MinFreeMB)
	_ = w.Flush()

	fmt.Fprintln(out, "\nSet these under Agents settings, start with memory_guard_mode=measure,")
	fmt.Fprintln(out, "and switch to enforce once the numbers look right.")
}

func sortedPIDs(m map[int]uint64) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// humanBytes renders a byte count the way an operator reads it.
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 3; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGT"[exp])
}
