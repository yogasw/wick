//go:build windows

package startupscript

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobHandles maps Cmd pointer -> JobObject handle so releaseProcessGroup
// can find and close the right handle on cleanup. Keyed by *exec.Cmd
// pointer identity — fine because each Run call has exactly one Cmd
// and never reuses it.
var (
	jobHandlesMu sync.Mutex
	jobHandles   = map[*exec.Cmd]windows.Handle{}
)

// applyProcessGroup creates a Job Object with KILL_ON_JOB_CLOSE and
// stashes the handle. assignToJob is called post-Start by the platform-
// agnostic glue. The handle has to outlive cmd.Start, so we keep it in
// a map keyed by *exec.Cmd until releaseProcessGroup runs.
//
// KILL_ON_JOB_CLOSE means: closing the handle terminates every process
// in the Job. Combined with TerminateJobObject on explicit kill, this
// guarantees a `Start-Process ngrok` inside the script can't outlive
// wick.
func applyProcessGroup(cmd *exec.Cmd) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	_, err = windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		windows.CloseHandle(h) //nolint:errcheck
		return
	}
	jobHandlesMu.Lock()
	jobHandles[cmd] = h
	jobHandlesMu.Unlock()
}

// assignToJob attaches the running process to the Job Object created
// in applyProcessGroup. Called by the platform-agnostic Run flow after
// cmd.Start so cmd.Process is populated. Returns silently on any
// failure — the worst case is the same orphan behaviour we had before
// process groups existed.
func assignToJob(cmd *exec.Cmd) {
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

// killProcessGroup terminates every process in the Job Object. Exit
// code 1 is conventional for "killed by parent".
func killProcessGroup(cmd *exec.Cmd) error {
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

// releaseProcessGroup closes the Job handle. KILL_ON_JOB_CLOSE means
// any survivor at this point gets terminated by the kernel. Safe to
// call multiple times — the second lookup finds nothing.
func releaseProcessGroup(cmd *exec.Cmd) {
	jobHandlesMu.Lock()
	h, ok := jobHandles[cmd]
	delete(jobHandles, cmd)
	jobHandlesMu.Unlock()
	if ok {
		windows.CloseHandle(h) //nolint:errcheck
	}
}
