//go:build linux

package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// proc_linux.go kills a gotty and everything under it. gotty's command
// runs in its own session (a PTY child calls setsid), so the process
// group alone does not reach it: the tree is walked through /proc.

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// procPPIDs maps every live pid to its parent.
func procPPIDs() map[int]int {
	out := map[int]int{}
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if st, ok := procStat(pid); ok {
			out[pid] = st.ppid
		}
	}
	return out
}

type statLine struct {
	state byte
	ppid  int
	start string // starttime in clock ticks; tells a reused pid apart
}

// procStat reads state and ppid from /proc/<pid>/stat. comm may hold
// spaces and parens, so fields are counted from the last ')'.
func procStat(pid int) (statLine, bool) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return statLine{}, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return statLine{}, false
	}
	f := strings.Fields(s[i+2:])
	if len(f) < 20 {
		return statLine{}, false
	}
	ppid, _ := strconv.Atoi(f[1])
	return statLine{state: f[0][0], ppid: ppid, start: f[19]}, true
}

// descendants lists every process under root, deepest last.
func descendants(root int) []int {
	children := map[int][]int{}
	for pid, ppid := range procPPIDs() {
		children[ppid] = append(children[ppid], pid)
	}
	var out []int
	queue := []int{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			out = append(out, c)
			queue = append(queue, c)
		}
	}
	return out
}

// alive: the pid exists and is not a zombie waiting to be reaped.
func alive(pid int) bool {
	st, ok := procStat(pid)
	return ok && st.state != 'Z' && st.state != 'X'
}

// procRef is a pid plus its start time.
type procRef struct {
	pid   int
	start string
}

// snapshot records root's descendants, so they can still be found after
// gotty dies and they are reparented away.
func snapshot(root int) []procRef {
	var out []procRef
	for _, p := range descendants(root) {
		if st, ok := procStat(p); ok {
			out = append(out, procRef{p, st.start})
		}
	}
	return out
}

// killKnown SIGKILLs snapshotted processes that are still the same
// process (same start time) and still alive.
func killKnown(refs []procRef) {
	for _, r := range refs {
		if st, ok := procStat(r.pid); ok && st.start == r.start && alive(r.pid) {
			_ = syscall.Kill(r.pid, syscall.SIGKILL)
		}
	}
}

// killTree asks gotty to stop (it SIGHUPs its command), waits briefly,
// then SIGKILLs whatever of the tree is left — the pids seen at the start
// plus anything spawned since.
func killTree(root int) {
	tree := append([]int{root}, descendants(root)...)
	_ = syscall.Kill(-root, syscall.SIGTERM)
	for _, p := range tree {
		_ = syscall.Kill(p, syscall.SIGTERM)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		any := false
		for _, p := range tree {
			if alive(p) {
				any = true
				break
			}
		}
		if !any {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	tree = append(tree, descendants(root)...)
	_ = syscall.Kill(-root, syscall.SIGKILL)
	for _, p := range tree {
		if alive(p) {
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
	}
}

// setSession puts cmd in its own session, like gotty's PTY child (tests).
func setSession(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
