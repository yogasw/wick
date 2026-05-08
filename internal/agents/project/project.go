// Package project manages on-disk project entries at
// `<BaseDir>/projects/<name>/`. A project is a master git clone (or
// `git init` workspace) plus metadata; sessions later attach via git
// worktree.
//
// Anything derivable from git or the filesystem (current branch, disk
// usage, attached sessions) stays out of meta.json — see
// agents-design.md §4.2.
package project

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// Meta is the persisted-on-disk shape of a project.
type Meta struct {
	RepoURL        string    `json:"repo_url,omitempty"`
	DefaultPreset  string    `json:"default_preset"`
	DefaultBackend string    `json:"default_backend,omitempty"`
	Description    string    `json:"description,omitempty"`
	Tags           []string  `json:"tags,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Project is the in-memory view returned by the registry: name (=
// folder) + persisted meta. Derived fields are computed on demand.
type Project struct {
	Name string `json:"name"`
	Meta Meta   `json:"meta"`
}

// CreateOptions describes a new project. A blank RepoURL means "no
// remote" — we still git-init a workspace so the worktree pattern
// works.
type CreateOptions struct {
	Name           string
	RepoURL        string
	DefaultPreset  string
	DefaultBackend string
	Description    string
	Tags           []string
}

// Create materializes the on-disk project: meta.json + workspace
// (clone or init). Refuses to overwrite an existing folder. Caller
// passes ctx so long clones can be canceled.
func Create(ctx context.Context, layout config.Layout, opt CreateOptions) (Project, error) {
	if err := storage.ValidateProjectName(opt.Name); err != nil {
		return Project{}, err
	}
	dir := layout.ProjectDir(opt.Name)
	if storage.PathExists(dir) {
		return Project{}, fmt.Errorf("project %q already exists", opt.Name)
	}
	if opt.DefaultPreset == "" {
		opt.DefaultPreset = "default"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Project{}, err
	}

	meta := Meta{
		RepoURL:        opt.RepoURL,
		DefaultPreset:  opt.DefaultPreset,
		DefaultBackend: opt.DefaultBackend,
		Description:    opt.Description,
		Tags:           opt.Tags,
		CreatedAt:      time.Now().UTC(),
	}
	if err := storage.WriteJSON(layout.ProjectMeta(opt.Name), &meta); err != nil {
		_ = os.RemoveAll(dir)
		return Project{}, err
	}
	if err := MaterializeWorkspace(ctx, layout.ProjectWorkspace(opt.Name), opt.RepoURL); err != nil {
		_ = os.RemoveAll(dir)
		return Project{}, fmt.Errorf("workspace setup failed: %w", err)
	}
	return Project{Name: opt.Name, Meta: meta}, nil
}

// Load reads projects/<name>/meta.json.
func Load(layout config.Layout, name string) (Project, error) {
	if err := storage.ValidateProjectName(name); err != nil {
		return Project{}, err
	}
	var meta Meta
	if err := storage.ReadJSON(layout.ProjectMeta(name), &meta); err != nil {
		return Project{}, err
	}
	return Project{Name: name, Meta: meta}, nil
}

// SaveMeta atomically rewrites projects/<name>/meta.json.
func SaveMeta(layout config.Layout, name string, meta Meta) error {
	if err := storage.ValidateProjectName(name); err != nil {
		return err
	}
	if !storage.PathExists(layout.ProjectDir(name)) {
		return fmt.Errorf("project %q not found", name)
	}
	return storage.WriteJSON(layout.ProjectMeta(name), &meta)
}

// List returns every project folder name, sorted.
func List(layout config.Layout) ([]string, error) {
	return storage.ScanDirNames(layout.ProjectsDir())
}

// Delete removes the entire project folder, including the master
// workspace clone. Caller is responsible for removing dependent
// session worktrees first — the registry wrapper handles that.
func Delete(layout config.Layout, name string) error {
	if err := storage.ValidateProjectName(name); err != nil {
		return err
	}
	return os.RemoveAll(layout.ProjectDir(name))
}

// Exists is a convenience used by registry / session create to verify
// an attach target before mutating session state.
func Exists(layout config.Layout, name string) bool {
	if storage.ValidateProjectName(name) != nil {
		return false
	}
	return storage.PathExists(layout.ProjectMeta(name))
}
