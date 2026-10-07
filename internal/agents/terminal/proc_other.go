//go:build !linux

package terminal

import (
	"os"
	"os/exec"
)

// Off linux there is no /proc walk: gotty is killed and its --once /
// --close-timeout take the command down with it.

func setProcGroup(*exec.Cmd) {}

func descendants(int) []int { return nil }

func alive(pid int) bool { return false }

type procRef struct{}

func snapshot(int) []procRef { return nil }

func killKnown([]procRef) {}

func killTree(root int) {
	if p, err := os.FindProcess(root); err == nil {
		_ = p.Kill()
	}
}

func setSession(*exec.Cmd) {}
