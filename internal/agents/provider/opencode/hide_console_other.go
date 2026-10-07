//go:build !windows

package opencode

import "os/exec"

func hideConsole(*exec.Cmd) {}
