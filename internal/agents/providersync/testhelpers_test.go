package providersync

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/yogasw/wick/internal/agents/provider"
)

// collectFiles is the legacy "load everything into a map" helper. The
// production sync path no longer uses it — backup() now streams files
// via syncFilePath to keep memory flat on large trees (see sync.go).
// Retained here in test scope so the existing path-normalisation and
// exclude-glob tests keep their tight, unit-level assertions.
func collectFiles(sc *provider.StorageConfig, excludes []string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	base := filepath.Clean(sc.SyncPath)
	if sc.Mode == "single" {
		abs := filepath.ToSlash(base)
		if matchesAnyExclude(abs, excludes) {
			return out, nil
		}
		data, err := os.ReadFile(base)
		if err != nil {
			return nil, err
		}
		out[abs] = data
		return out, nil
	}
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		abs := filepath.ToSlash(path)
		if d.IsDir() {
			if matchesAnyExclude(abs, excludes) {
				return filepath.SkipDir
			}
			return nil
		}
		if matchesAnyExclude(abs, excludes) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Mode()&os.ModeType != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		out[abs] = data
		return nil
	})
	return out, err
}
