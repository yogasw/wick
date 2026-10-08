//go:build !windows

package schedule

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// Resource limits of a bash step, set before the script starts (prlimit
// execs into bash with them, so nothing runs unlimited even for an instant).
const (
	// stepMaxAddressSpace caps virtual memory: an allocation loop fails
	// inside the script instead of pushing the host into OOM.
	stepMaxAddressSpace = 1 << 30
	// stepProcHeadroom is how many processes a script may add. RLIMIT_NPROC
	// counts every process of the uid — the daemon and every agent included
	// — so the cap is "what the uid runs now + headroom"; a flat 64 would
	// make the script's very first fork fail on a busy host.
	stepProcHeadroom = 256
	stepMaxFileSize  = 64 << 20
)

// prlimitBin is prlimit's path, "" when the host has none (then a bash
// ulimit preamble sets the same limits).
var prlimitBin = func() string {
	p, _ := exec.LookPath("prlimit")
	return p
}()

// limitedBash is the command that runs script under the step's limits:
// address space, process count, CPU seconds a little past its timeout and
// the size of any file it writes.
func limitedBash(ctx context.Context, script string, timeoutSec int) *exec.Cmd {
	cpu := strconv.Itoa(timeoutSec + 5)
	as := strconv.Itoa(stepMaxAddressSpace)
	fsize := strconv.Itoa(stepMaxFileSize)
	nproc := ""
	if n := uidProcCount(); n > 0 {
		nproc = strconv.Itoa(n + stepProcHeadroom)
	}
	if prlimitBin != "" {
		args := []string{"--as=" + as, "--cpu=" + cpu, "--fsize=" + fsize}
		if nproc != "" {
			args = append(args, "--nproc="+nproc)
		}
		args = append(args, "--", "bash", "-c", script)
		return exec.CommandContext(ctx, prlimitBin, args...)
	}
	// ulimit -v and -f take KiB; the script runs as $1 of an exec'd bash.
	pre := "ulimit -v " + strconv.Itoa(stepMaxAddressSpace/1024) + " -t " + cpu + " -f " + strconv.Itoa(stepMaxFileSize/1024)
	if nproc != "" {
		pre += " -u " + nproc
	}
	return exec.CommandContext(ctx, "bash", "-c", pre+` || exit 126; exec bash -c "$1"`, "wick-watch", script)
}

// uidProcCount counts the processes (threads included, as RLIMIT_NPROC
// does) owned by this process's uid; 0 when /proc cannot be read.
func uidProcCount() int {
	uid := uint32(os.Getuid())
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		fi, err := os.Stat("/proc/" + e.Name() + "/task")
		if err != nil {
			continue
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uid {
			continue
		}
		if tasks, err := os.ReadDir("/proc/" + e.Name() + "/task"); err == nil {
			n += len(tasks)
		}
	}
	return n
}

// setProcessGroup puts the script in its own process group and makes a
// timeout kill the whole group, so a `sleep` or a pipeline the script spawned
// does not outlive it.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
