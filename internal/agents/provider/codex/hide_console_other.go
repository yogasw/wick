//go:build !windows

package codex

import "os/exec"

func hideConsole(*exec.Cmd) {}
