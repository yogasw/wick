//go:build !windows && !headless

package systemtray

import (
	"os/exec"
	"runtime"
)

func openInEditor(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Start()
	}
	return exec.Command("xdg-open", path).Start()
}
