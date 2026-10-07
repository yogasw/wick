//go:build linux

package managedbin

import (
	"os"
	"path/filepath"
	"strings"
)

// scanInUse counts running processes per installed version by reading
// /proc/<pid>/exe. A process keeps pointing at the file it was started
// from even after `current` moves, which is exactly the "N sessions still
// on vX" signal — no bookkeeping in the spawn path can drift from it.
func scanInUse(versionsDir string) map[string]int {
	out := map[string]int{}
	prefix := filepath.Clean(versionsDir) + string(os.PathSeparator)
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, e := range ents {
		name := e.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		exe, err := os.Readlink(filepath.Join("/proc", name, "exe"))
		if err != nil {
			continue
		}
		exe = strings.TrimSuffix(exe, " (deleted)")
		if !strings.HasPrefix(exe, prefix) {
			continue
		}
		ver := strings.SplitN(strings.TrimPrefix(exe, prefix), string(os.PathSeparator), 2)[0]
		out[ver]++
	}
	return out
}
