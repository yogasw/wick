package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// climodels_disk.go keeps the live model list (cliModelsCache) on disk, so
// a restart, a reload or the next spawn starts from the list it last knew
// instead of reading it again. The file is only a starting point: the
// in-memory rules still apply on top of it — a changed account (authStamp)
// re-harvests, Refresh runs the CLI, Invalidate drops it — and every one of
// those writes the file back.

// cliModelsDir is where the lists live; "" turns persistence off. Off under
// `go test` unless a test points it somewhere, so no test reads or writes
// the operator's real lists.
var cliModelsDir = func() string {
	if testing.Testing() {
		return ""
	}
	dir, err := modelStateDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "live-models")
}

// cliModelsDisk is one instance's persisted entry. Key guards against a
// file left by an instance of the same name with another profile / data
// dir / binary: a mismatch is ignored.
type cliModelsDisk struct {
	Key       string      `json:"key"`
	Models    []ModelSeed `json:"models"`
	At        time.Time   `json:"at"`
	Source    string      `json:"source,omitempty"`
	AuthStamp string      `json:"auth_stamp,omitempty"`
}

func cliModelsFile(ins Instance) string {
	dir := cliModelsDir()
	if dir == "" {
		return ""
	}
	name := strings.NewReplacer("/", "_", `\`, "_", "..", "_").Replace(ins.Name)
	return filepath.Join(dir, string(ins.Type)+"-"+name+".json")
}

// cliModelsLookup is cliModelsCache[key], filled from disk on a miss.
// Callers hold cliModelsMu.
func cliModelsLookup(ins Instance, key string) (cliModelsEntry, bool) {
	if e, ok := cliModelsCache[key]; ok {
		return e, true
	}
	path := cliModelsFile(ins)
	if path == "" {
		return cliModelsEntry{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cliModelsEntry{}, false
	}
	var d cliModelsDisk
	if json.Unmarshal(b, &d) != nil || d.Key != key || len(d.Models) == 0 {
		return cliModelsEntry{}, false
	}
	e := cliModelsEntry{models: d.Models, at: d.At, source: d.Source, authStamp: d.AuthStamp}
	cliModelsCache[key] = e
	return e, true
}

// cliModelsStore sets the entry and writes it through. A failed fetch is
// kept in memory only: the file holds the last good list. Callers hold
// cliModelsMu.
func cliModelsStore(ins Instance, key string, e cliModelsEntry) {
	cliModelsCache[key] = e
	if e.err != nil || len(e.models) == 0 {
		return
	}
	path := cliModelsFile(ins)
	if path == "" {
		return
	}
	b, err := json.Marshal(cliModelsDisk{Key: key, Models: e.models, At: e.at, Source: e.source, AuthStamp: e.authStamp})
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// cliModelsDrop forgets the entry in memory and on disk. Callers hold
// cliModelsMu.
func cliModelsDrop(ins Instance, key string) {
	delete(cliModelsCache, key)
	if path := cliModelsFile(ins); path != "" {
		_ = os.Remove(path)
	}
}
