package storage

import (
	"errors"
	"os"
	"sort"
	"strings"
)

// ScanDirNames returns the immediate sub-directory names of dir,
// sorted, with hidden (`.`-prefixed) entries skipped. Returns an empty
// slice if dir doesn't exist (treat as "nothing yet").
func ScanDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// PathExists reports whether path exists (file or dir).
func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
