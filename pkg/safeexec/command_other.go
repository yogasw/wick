//go:build !windows

package safeexec

import "os/exec"

// fixBatchQuoting is a no-op off Windows: only cmd.exe's batch-file
// argument parsing needs the special quoting workaround.
func fixBatchQuoting(*exec.Cmd) {}
