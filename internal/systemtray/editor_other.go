//go:build !windows && !headless

package systemtray

import (
	"runtime"

	"github.com/yogasw/wick/internal/safeexec"
)

func openInEditor(path string) error {
	if runtime.GOOS == "darwin" {
		return safeexec.Command("open", path).Start()
	}
	return safeexec.Command("xdg-open", path).Start()
}
