//go:build windows

package proctree

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobHandles maps *exec.Cmd -> Job Object handle so kill/release can find
// the right handle. Keyed by pointer identity — safe because each command
// has exactly one Cmd that is never reused.
var (
	jobHandlesMu sync.Mutex
	jobHandles   = map[*exec.Cmd]windows.Handle{}
)

// apply creates a Job Object with KILL_ON_JOB_CLOSE and stashes the
// handle keyed by cmd. The handle must outlive cmd.Start, hence the map;
// assign attaches the process to it post-Start. Returns silently on any
// failure — degrades to the pre-proctree orphan behaviour.
//
// KILL_ON_JOB_CLOSE means closing the handle terminates every process in
// the Job. Combined with TerminateJobObject on explicit kill, a
// `Start-Process` / forked child inside the command can't outlive us.
func apply(cmd *exec.Cmd) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(h) //nolint:errcheck
		return
	}
	jobHandlesMu.Lock()
	jobHandles[cmd] = h
	jobHandlesMu.Unlock()
}

// assign attaches the running process to the Job Object created in apply.
// Call after cmd.Start so cmd.Process is populated. Silent on failure.
func assign(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	jobHandlesMu.Lock()
	h, ok := jobHandles[cmd]
	jobHandlesMu.Unlock()
	if !ok {
		return
	}
	procH, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return
	}
	defer windows.CloseHandle(procH) //nolint:errcheck
	_ = windows.AssignProcessToJobObject(h, procH)
}

// killGroup terminates every process in the Job Object. Exit code 1 is
// the conventional "killed by parent". Falls back to a single-PID kill
// when no Job handle was set up (apply failed).
func killGroup(cmd *exec.Cmd) error {
	jobHandlesMu.Lock()
	h, ok := jobHandles[cmd]
	jobHandlesMu.Unlock()
	if !ok {
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return windows.TerminateJobObject(h, 1)
}

// release closes the Job handle. KILL_ON_JOB_CLOSE reaps any survivor at
// this point. Idempotent — a second call finds nothing.
func release(cmd *exec.Cmd) {
	jobHandlesMu.Lock()
	h, ok := jobHandles[cmd]
	delete(jobHandles, cmd)
	jobHandlesMu.Unlock()
	if ok {
		windows.CloseHandle(h) //nolint:errcheck
	}
}
