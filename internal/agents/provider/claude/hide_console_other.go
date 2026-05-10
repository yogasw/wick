//go:build !windows

package claude

import "os/exec"

func hideConsole(*exec.Cmd) {}
