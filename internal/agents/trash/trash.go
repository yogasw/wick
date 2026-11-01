// Package trash keeps deleted projects recoverable for a while.
//
// A project delete moves everything it used to remove into one entry
// under `<agents root>/trash/projects/<id>__<unix>/`:
//
//	trash.json          the manifest: who, when, and where each part came from
//	project/            the project folder (meta.json, tickets/, files/)
//	sessions/<sid>/     every top-level session folder (sub-agents live inside)
//	provider/<n>/       provider history folders (claude transcripts, …)
//
// Restore moves every part back to the path the manifest recorded and
// refuses when one of them is occupied. Purge removes an entry for good;
// PurgeOlderThan is the retention sweep.
//
// The package only moves folders. Stopping agents, cancelling schedules
// and telling the registry belong to the caller.
package trash

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Retention is how long a deleted project stays restorable before the
// sweep removes it for good. A constant, not a setting: agents have no
// settings store this belongs in, and one window for everyone keeps the
// "restorable for 30 days" promise in the delete dialog true.
const Retention = 30 * 24 * time.Hour

// ManifestFile is the manifest's name inside an entry.
const ManifestFile = "trash.json"

// ErrNotFound means no trash entry has that key.
var ErrNotFound = errors.New("trash entry not found")

// ErrOccupied means a restore target already exists.
var ErrOccupied = errors.New("restore target is occupied")

// Moved is one folder moved into an entry: Stored is relative to the
// entry, Original is the absolute path it came from.
type Moved struct {
	Original string `json:"original"`
	Stored   string `json:"stored"`
}

// ProviderMoved is a provider history folder moved into an entry.
type ProviderMoved struct {
	Provider string `json:"provider"`
	Moved
}

// Schedule is a schedule the delete cancelled, kept so a human can
// recreate it: restore does not bring schedules back.
type Schedule struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	SessionID string    `json:"session_id,omitempty"`
	Cron      string    `json:"cron,omitempty"`
	EveryMs   int64     `json:"interval_ms,omitempty"`
	RunAt     time.Time `json:"run_at"`
	Message   string    `json:"message"`
}

// Manifest describes one trash entry.
type Manifest struct {
	Key         string          `json:"key"`
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	OwnerUserID string          `json:"owner_user_id,omitempty"`
	DeletedBy   string          `json:"deleted_by,omitempty"`
	DeletedAt   time.Time       `json:"deleted_at"`
	CustomPath  string          `json:"custom_path,omitempty"`
	Project     Moved           `json:"project"`
	SessionIDs  []string        `json:"session_ids"`
	Sessions    []Moved         `json:"sessions"`
	Providers   []ProviderMoved `json:"providers"`
	Schedules   []Schedule      `json:"schedules"`
}

// Stash is what Put moves and records.
type Stash struct {
	ID, Name, OwnerUserID, DeletedBy, CustomPath string
	// ProjectDir is the project folder; SessionDirs maps every session
	// id of the project (sub-agents included) to its folder. A folder
	// already gone — a sub-agent moved with its parent — is skipped.
	ProjectDir  string
	SessionDirs map[string]string
	// Providers maps a provider name to the history folders to move.
	Providers map[string][]string
	Schedules []Schedule
}

// Root is the projects trash under the agents root.
func Root(base string) string { return filepath.Join(base, "trash", "projects") }

// EntryKey names the entry of project id deleted at t.
func EntryKey(id string, t time.Time) string { return id + "__" + strconv.FormatInt(t.Unix(), 10) }

func entryDir(base, key string) (string, error) {
	if key == "" || key != filepath.Base(key) || strings.HasPrefix(key, ".") {
		return "", ErrNotFound
	}
	return filepath.Join(Root(base), key), nil
}

// Put moves the project, its sessions and provider history into a new
// entry and writes the manifest. The project folder goes first: when it
// cannot move nothing else is touched. A session or provider folder that
// fails to move is left where it is and reported in the returned error
// next to a manifest of what did move.
func Put(base string, now time.Time, s Stash) (Manifest, error) {
	m := Manifest{
		Key: EntryKey(s.ID, now), ID: s.ID, Name: s.Name, OwnerUserID: s.OwnerUserID,
		DeletedBy: s.DeletedBy, DeletedAt: now.UTC(), CustomPath: s.CustomPath,
		SessionIDs: []string{}, Sessions: []Moved{}, Providers: []ProviderMoved{}, Schedules: s.Schedules,
	}
	if m.Schedules == nil {
		m.Schedules = []Schedule{}
	}
	dir, err := entryDir(base, m.Key)
	if err != nil {
		return m, err
	}
	if _, err := os.Stat(dir); err == nil {
		return m, fmt.Errorf("trash entry %s already exists", m.Key)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return m, err
	}
	m.Project = Moved{Original: s.ProjectDir, Stored: "project"}
	if err := Move(s.ProjectDir, filepath.Join(dir, "project")); err != nil {
		_ = os.RemoveAll(dir)
		return m, fmt.Errorf("move project folder: %w", err)
	}
	var errs []error
	// Parents before children (a shorter id first), so a nested sub-agent
	// is found gone once its parent moved and is not moved twice.
	sids := make([]string, 0, len(s.SessionDirs))
	for sid := range s.SessionDirs {
		sids = append(sids, sid)
	}
	sort.Slice(sids, func(i, j int) bool {
		if len(sids[i]) != len(sids[j]) {
			return len(sids[i]) < len(sids[j])
		}
		return sids[i] < sids[j]
	})
	m.SessionIDs = sids
	for _, sid := range sids {
		src := s.SessionDirs[sid]
		if _, err := os.Stat(src); err != nil {
			continue
		}
		mv := Moved{Original: src, Stored: filepath.Join("sessions", sid)}
		if err := Move(src, filepath.Join(dir, mv.Stored)); err != nil {
			errs = append(errs, fmt.Errorf("session %s: %w", sid, err))
			continue
		}
		m.Sessions = append(m.Sessions, mv)
	}
	names := make([]string, 0, len(s.Providers))
	for name := range s.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, src := range s.Providers[name] {
			mv := ProviderMoved{Provider: name, Moved: Moved{
				Original: src,
				Stored:   filepath.Join("provider", strconv.Itoa(len(m.Providers))+"-"+filepath.Base(src)),
			}}
			if err := Move(src, filepath.Join(dir, mv.Stored)); err != nil {
				errs = append(errs, fmt.Errorf("provider %s %s: %w", name, src, err))
				continue
			}
			m.Providers = append(m.Providers, mv)
		}
	}
	if err := writeManifest(dir, m); err != nil {
		errs = append(errs, err)
	}
	return m, errors.Join(errs...)
}

func writeManifest(dir string, m Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ManifestFile), b, 0o600)
}

// Load reads the manifest of one entry.
func Load(base, key string) (Manifest, error) {
	dir, err := entryDir(base, key)
	if err != nil {
		return Manifest{}, err
	}
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, ErrNotFound
	}
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("trash %s: %w", key, err)
	}
	m.Key = key
	return m, nil
}

// List returns every readable entry, newest first. An entry without a
// readable manifest is skipped: it cannot be restored or attributed.
func List(base string) ([]Manifest, error) {
	ents, err := os.ReadDir(Root(base))
	if errors.Is(err, os.ErrNotExist) {
		return []Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Manifest{}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if m, err := Load(base, e.Name()); err == nil {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt.After(out[j].DeletedAt) })
	return out, nil
}

// Restore moves every part of the entry back to its original path and
// removes the entry. Every target is checked before anything moves, so a
// refused restore leaves the entry whole.
func Restore(base, key string) (Manifest, error) {
	m, err := Load(base, key)
	if err != nil {
		return m, err
	}
	dir, _ := entryDir(base, key)
	parts := []Moved{m.Project}
	parts = append(parts, m.Sessions...)
	for _, p := range m.Providers {
		parts = append(parts, p.Moved)
	}
	for _, p := range parts {
		if p.Original == "" {
			return m, fmt.Errorf("trash %s: a part has no original path", key)
		}
		if _, err := os.Lstat(p.Original); err == nil {
			return m, fmt.Errorf("%w: %s", ErrOccupied, p.Original)
		}
	}
	for _, p := range parts {
		if err := os.MkdirAll(filepath.Dir(p.Original), 0o755); err != nil {
			return m, err
		}
		if err := Move(filepath.Join(dir, p.Stored), p.Original); err != nil {
			return m, fmt.Errorf("restore %s: %w", p.Original, err)
		}
	}
	return m, os.RemoveAll(dir)
}

// Purge removes an entry for good and returns its manifest.
func Purge(base, key string) (Manifest, error) {
	m, err := Load(base, key)
	if err != nil {
		return m, err
	}
	dir, _ := entryDir(base, key)
	return m, os.RemoveAll(dir)
}

// PurgeOlderThan purges every entry deleted before now-window and
// returns what it removed. One failure does not stop the sweep.
func PurgeOlderThan(base string, now time.Time, window time.Duration) ([]Manifest, error) {
	all, err := List(base)
	if err != nil {
		return nil, err
	}
	var gone []Manifest
	var errs []error
	for _, m := range all {
		if now.Sub(m.DeletedAt) < window {
			continue
		}
		if _, err := Purge(base, m.Key); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.Key, err))
			continue
		}
		gone = append(gone, m)
	}
	return gone, errors.Join(errs...)
}

// Move renames src to dst, creating dst's parent. Across filesystems,
// where rename fails, it copies the tree and then removes src.
func Move(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyTree(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	return os.RemoveAll(src)
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		}
		return nil // sockets, fifos: nothing worth keeping
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
