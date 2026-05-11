//go:build !windows

package gemini

import "os/exec"

func hideConsole(*exec.Cmd) {}
