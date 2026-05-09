// Package workspace manages on-disk workspace entries at
// `<BaseDir>/workspaces/<name>/` (managed) or any user-supplied
// absolute path (custom).
//
// A workspace is a plain folder used as the cwd for agent
// subprocesses. It is intentionally git-agnostic — wick does not
// clone, init, or branch. Whatever the agent (or user) puts in the
// folder is the workspace's contents. Multiple sessions may share
// the same workspace and run in parallel; coordination is the
// caller's concern.
//
// See agents-design.md §0.2 for the refactor that introduced this
// package (replacing the project-centric model).
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// Meta is the persisted-on-disk shape of a workspace.
//
// CustomPath is the absolute path supplied by the user when they
// wanted to point at an existing folder on disk. Empty means
// managed (resolved via Layout.WorkspaceManagedPath).
type Meta struct {
	CustomPath     string    `json:"custom_path,omitempty"`
	DefaultPreset  string    `json:"default_preset"`
	DefaultProvider string    `json:"default_provider,omitempty"`
	Description    string    `json:"description,omitempty"`
	Tags           []string  `json:"tags,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Workspace is the in-memory view returned by the registry: name (=
// folder under workspaces/) + persisted meta.
type Workspace struct {
	Name string `json:"name"`
	Meta Meta   `json:"meta"`
}

// CreateOptions describes a new workspace.
//
// CustomPath, when non-empty, must be an absolute path to an
// existing directory. The directory is not modified; wick only
// records a pointer to it. Leave empty to use the managed location.
type CreateOptions struct {
	Name           string
	CustomPath     string
	DefaultPreset  string
	DefaultProvider string
	Description    string
	Tags           []string
}

// Create materializes the on-disk workspace entry: the metadata
// folder under `workspaces/<name>/` and, if managed, the empty
// content folder under `workspaces/<name>/files/`.
//
// Custom paths are not auto-created — the user is expected to point
// at a folder that already exists. Refusing missing custom paths
// surfaces typos at create time rather than at first spawn.
func Create(layout config.Layout, opt CreateOptions) (Workspace, error) {
	if err := storage.ValidateWorkspaceName(opt.Name); err != nil {
		return Workspace{}, err
	}
	dir := layout.WorkspaceDir(opt.Name)
	if storage.PathExists(dir) {
		return Workspace{}, fmt.Errorf("workspace %q already exists", opt.Name)
	}
	if opt.DefaultPreset == "" {
		opt.DefaultPreset = "default"
	}
	if opt.CustomPath != "" {
		if !filepath.IsAbs(opt.CustomPath) {
			return Workspace{}, fmt.Errorf("custom path must be absolute: %q", opt.CustomPath)
		}
		if !storage.PathExists(opt.CustomPath) {
			return Workspace{}, fmt.Errorf("custom path does not exist: %q", opt.CustomPath)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Workspace{}, err
	}
	meta := Meta{
		CustomPath:     opt.CustomPath,
		DefaultPreset:  opt.DefaultPreset,
		DefaultProvider: opt.DefaultProvider,
		Description:    opt.Description,
		Tags:           opt.Tags,
		CreatedAt:      time.Now().UTC(),
	}
	if err := storage.WriteJSON(layout.WorkspaceMeta(opt.Name), &meta); err != nil {
		_ = os.RemoveAll(dir)
		return Workspace{}, err
	}
	if opt.CustomPath == "" {
		if err := os.MkdirAll(layout.WorkspaceManagedPath(opt.Name), 0o755); err != nil {
			_ = os.RemoveAll(dir)
			return Workspace{}, err
		}
	}
	return Workspace{Name: opt.Name, Meta: meta}, nil
}

// Load reads workspaces/<name>/meta.json.
func Load(layout config.Layout, name string) (Workspace, error) {
	if err := storage.ValidateWorkspaceName(name); err != nil {
		return Workspace{}, err
	}
	var meta Meta
	if err := storage.ReadJSON(layout.WorkspaceMeta(name), &meta); err != nil {
		return Workspace{}, err
	}
	return Workspace{Name: name, Meta: meta}, nil
}

// SaveMeta atomically rewrites workspaces/<name>/meta.json.
func SaveMeta(layout config.Layout, name string, meta Meta) error {
	if err := storage.ValidateWorkspaceName(name); err != nil {
		return err
	}
	if !storage.PathExists(layout.WorkspaceDir(name)) {
		return fmt.Errorf("workspace %q not found", name)
	}
	return storage.WriteJSON(layout.WorkspaceMeta(name), &meta)
}

// List returns every workspace name, sorted.
func List(layout config.Layout) ([]string, error) {
	return storage.ScanDirNames(layout.WorkspacesDir())
}

// Delete removes the workspace metadata folder. For managed
// workspaces this also removes `workspaces/<name>/files/`. For
// custom workspaces the user-supplied path is left untouched —
// wick never owned it, so wick must not delete it.
func Delete(layout config.Layout, name string) error {
	if err := storage.ValidateWorkspaceName(name); err != nil {
		return err
	}
	return os.RemoveAll(layout.WorkspaceDir(name))
}

// Exists reports whether a workspace with the given name has been
// created.
func Exists(layout config.Layout, name string) bool {
	if storage.ValidateWorkspaceName(name) != nil {
		return false
	}
	return storage.PathExists(layout.WorkspaceMeta(name))
}

// ResolvePath returns the cwd for agent subprocesses bound to the
// named workspace. Custom paths win over the managed default.
//
// The returned path is guaranteed to be absolute. It is NOT created
// here — callers (the pool) must MkdirAll managed paths before
// passing them to exec.Cmd.Dir. Custom paths are validated at
// Create time and assumed to still exist; if the user deleted them
// out from under wick, spawn will surface a clean error.
func ResolvePath(layout config.Layout, name string) (string, error) {
	ws, err := Load(layout, name)
	if err != nil {
		return "", err
	}
	if ws.Meta.CustomPath != "" {
		return ws.Meta.CustomPath, nil
	}
	return layout.WorkspaceManagedPath(name), nil
}
