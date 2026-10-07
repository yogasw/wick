package managedbin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VersionInfo is one installed version as recorded in state.json.
type VersionInfo struct {
	Version       string    `json:"version"`
	Tag           string    `json:"tag"`
	Asset         string    `json:"asset"`
	SHA256        string    `json:"sha256"` // of the installed binary file
	AssetSHA256   string    `json:"asset_sha256"`
	Size          int64     `json:"size"`
	InstalledAt   time.Time `json:"installed_at"`
	Host          Host      `json:"host"`
	VersionOutput string    `json:"version_output"`
}

type state struct {
	Versions map[string]VersionInfo `json:"versions"`
	LastJob  *JobInfo               `json:"last_job,omitempty"`
}

func (m *Manager) typeDir(typ string) string     { return filepath.Join(m.root(), typ) }
func (m *Manager) versionsDir(typ string) string { return filepath.Join(m.typeDir(typ), "versions") }
func (m *Manager) statePath(typ string) string   { return filepath.Join(m.typeDir(typ), "state.json") }
func (m *Manager) currentPath(typ string) string { return filepath.Join(m.typeDir(typ), "current") }

func (m *Manager) binPath(typ, ver, binary string) string {
	return filepath.Join(m.versionsDir(typ), ver, binary)
}

func (m *Manager) loadState(typ string) state {
	st := state{Versions: map[string]VersionInfo{}}
	b, err := os.ReadFile(m.statePath(typ))
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	if st.Versions == nil {
		st.Versions = map[string]VersionInfo{}
	}
	return st
}

func (m *Manager) saveState(typ string, st state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(m.statePath(typ), b)
}

func (m *Manager) readCurrent(typ string) string {
	b, err := os.ReadFile(m.currentPath(typ))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// writeAtomic replaces path via a temp file + rename, so a reader never
// sees a half-written `current` or state.json.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// validVersion keeps a version string safe to use as a directory name.
func validVersion(v string) error {
	if v == "" || strings.ContainsAny(v, `/\`) || v == "." || v == ".." || strings.HasPrefix(v, ".") {
		return errors.New("invalid version " + v)
	}
	return nil
}
