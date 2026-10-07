//go:build !windows

package omp

import "os/exec"

func hideConsole(*exec.Cmd) {}
