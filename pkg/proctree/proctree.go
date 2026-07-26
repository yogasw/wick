// Package proctree runs a child process as a whole killable tree.
//
// The Go stdlib kills only the direct child (exec.CommandContext's
// cancel, or Process.Kill). A shell almost always spawns descendants —
// `sleep 5` in Git Bash forks sleep.exe; `npm i` forks node; `ngrok &`
// backgrounds a daemon — and those survive a parent-only kill. On
// Windows they even keep the stdout pipe open, so a CombinedOutput never
// returns and a "timeout" fires on paper but the work runs to completion
// anyway.
//
// proctree wraps the OS primitives that make the whole tree die together:
// a POSIX process group (Setpgid + kill(-pgid)) on Unix, and a Job Object
// with KILL_ON_JOB_CLOSE on Windows. It is the extracted, reusable form
// of the logic internal/startupscript has used for `ngrok &` cleanup.
//
// Usage — the four calls must bracket the process lifecycle in order:
//
//	cmd := safeexec.Command(bin, args...)   // NOT CommandContext — we own the kill
//	proctree.Apply(cmd)                     // before Start: configure the group
//	cmd.Start()
//	proctree.Assign(cmd)                    // after Start: attach (Windows)
//	defer proctree.Release(cmd)             // after Wait: free the handle
//	// on ctx cancel / deadline: proctree.Kill(cmd) — reaps every descendant
package proctree

import "os/exec"

// Apply configures cmd so its process and every descendant land in one
// killable group. Call BEFORE cmd.Start. No-op-safe if the platform
// primitive can't be set up — the worst case is the pre-proctree
// orphan behaviour.
func Apply(cmd *exec.Cmd) { apply(cmd) }

// Assign attaches the running process to its group container. Call AFTER
// cmd.Start (cmd.Process must be populated). No-op on Unix, where the
// group is established at fork by Setpgid; required on Windows to add the
// PID to the Job Object.
func Assign(cmd *exec.Cmd) { assign(cmd) }

// Kill terminates every process in cmd's group — the child and all its
// descendants. Use this instead of cmd.Process.Kill when the command may
// have spawned sub-processes. Safe to call after the process already
// exited (returns nil).
func Kill(cmd *exec.Cmd) error { return killGroup(cmd) }

// Release frees any OS handle held for the group. Call AFTER cmd.Wait,
// typically via defer. No-op on Unix (groups vanish when empty); on
// Windows it closes the Job handle, and KILL_ON_JOB_CLOSE reaps any
// straggler. Idempotent.
func Release(cmd *exec.Cmd) { release(cmd) }
