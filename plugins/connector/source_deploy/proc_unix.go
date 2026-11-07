//go:build !windows

package main

import (
	"errors"
	"os"
	"strings"
	"syscall"

	"github.com/yogasw/wick/pkg/safeexec"
)

// wrapper runs the script and writes its exit code to $WICK_DEPLOY_EXIT. The script is
// not wick's child for long: wick is the thing it replaces, so the exit code has to land
// in a file the next wick reads, not in a Wait() nobody is left to call.
const wrapper = `"$@"; code=$?; echo "$code" > "$WICK_DEPLOY_EXIT"; "$WICK_DEPLOY_NOTIFY_EXE" --notify-run || true; exit "$code"`

// launch starts the script detached and returns its pid.
//
// Under a systemd unit the script would share wick's cgroup, and `systemctl restart` (or
// the script's own stop / swap / start on a daemon without graceful upgrade) kills the
// whole cgroup — the deploy would die halfway through the swap it is doing. So when the
// user manager is reachable it runs in its own transient scope; elsewhere (Termux, a
// plain `<app> start`, a system unit with no user bus) a new session is enough.
func launch(script string, args []string, exitFile, runID string, logf *os.File) (int, error) {
	interp, err := interpreter(script)
	if err != nil {
		return 0, err
	}
	sh, err := safeexec.LookPath("sh")
	if err != nil {
		return 0, errors.New("no sh on PATH")
	}
	argv := append([]string{sh, "-c", wrapper, "wick-deploy", interp, script}, args...)
	if inSystemdUnit() && userScopeWorks() {
		if run, err := safeexec.LookPath("systemd-run"); err == nil {
			argv = append([]string{run, "--user", "--scope", "--collect", "--quiet"}, argv...)
		}
	}
	cmd := safeexec.Command(argv[0], argv[1:]...)
	cmd.Env = childEnv(exitFile, runID)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if dir, err := os.UserHomeDir(); err == nil {
		cmd.Dir = dir
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Reap it if it ends while this wick is still up; the exit file is the record.
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

func interpreter(script string) (string, error) {
	name := "bash"
	if strings.HasSuffix(strings.ToLower(script), ".ps1") {
		name = "pwsh"
	}
	p, err := safeexec.LookPath(name)
	if err != nil {
		return "", errors.New(name + " is not installed: the deploy script needs it")
	}
	return p, nil
}

func inSystemdUnit() bool { return os.Getenv("INVOCATION_ID") != "" }

// userScopeWorks: a transient user scope needs a user manager this process can reach,
// which a system unit running as a normal user often does not have.
func userScopeWorks() bool {
	run, err := safeexec.LookPath("systemd-run")
	if err != nil {
		return false
	}
	return safeexec.Command(run, "--user", "--scope", "--collect", "--quiet", "true").Run() == nil
}

// killTree stops the script and everything it started: it leads its own session, so its
// pid is also its process group.
func killTree(pid int) error {
	if pid <= 0 {
		return errors.New("no pid")
	}
	return syscall.Kill(-pid, syscall.SIGTERM)
}

// processAlive: signal 0 checks the pid exists; EPERM still means it does.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
