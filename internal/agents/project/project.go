// Package project manages on-disk project entries at
// `<BaseDir>/projects/<id>/` (managed) or any user-supplied absolute
// path (custom).
//
// A Project is a bundle of: 1 folder (the agent cwd), defaults
// (preset/provider/system_addon), pinned sessions, icon, and display
// name. Sessions reference a project via Meta.ProjectID.
//
// Storage layout:
//
//	projects/<id>/
//	  meta.json        — project meta (this package owns it)
//	  files/           — managed cwd (only when CustomPath is empty)
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// Defaults holds the preset/provider/system_addon that new sessions
// in this project inherit when not explicitly overridden.
type Defaults struct {
	Preset      string `json:"preset,omitempty"`
	Provider    string `json:"provider,omitempty"`
	SystemAddon string `json:"system_addon,omitempty"`
}

// Meta is the persisted shape of a project.
type Meta struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Icon           string    `json:"icon,omitempty"`
	Description    string    `json:"description,omitempty"`
	CustomPath     string    `json:"custom_path,omitempty"`
	Defaults       Defaults  `json:"defaults"`
	PinnedSessions []string  `json:"pinned_sessions,omitempty"`
	Tags           []string  `json:"tags,omitempty"`
	OwnerUserID    string    `json:"owner_user_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Project is the in-memory view: meta only (no session list — that lives
// in the registry).
type Project struct {
	Meta Meta `json:"meta"`
}

// ID returns the project's UUID.
func (p Project) ID() string { return p.Meta.ID }

// CreateOptions describes a new project.
type CreateOptions struct {
	ID          string // pre-assigned UUID; generated if empty
	Name        string
	Icon        string
	Description string
	CustomPath  string
	Defaults    Defaults
	Tags        []string
	OwnerUserID string
}

// Create materialises the on-disk project entry.
// For managed projects (CustomPath=="") it also creates projects/<id>/files/.
// Custom paths are not created — they must already exist.
func Create(layout config.Layout, opt CreateOptions) (Project, error) {
	if opt.ID == "" {
		return Project{}, fmt.Errorf("project id is required")
	}
	if opt.Name == "" {
		return Project{}, fmt.Errorf("project name is required")
	}
	if storage.PathExists(layout.ProjectMeta(opt.ID)) {
		return Project{}, fmt.Errorf("project %q already exists", opt.ID)
	}
	if opt.CustomPath != "" {
		if !filepath.IsAbs(opt.CustomPath) {
			return Project{}, fmt.Errorf("custom path must be absolute: %q", opt.CustomPath)
		}
		if !storage.PathExists(opt.CustomPath) {
			return Project{}, fmt.Errorf("custom path does not exist: %q", opt.CustomPath)
		}
	}
	if opt.Icon == "" {
		opt.Icon = "📁"
	}
	if opt.Defaults.Preset == "" {
		opt.Defaults.Preset = "default"
	}

	dir := layout.ProjectDir(opt.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Project{}, err
	}

	now := time.Now().UTC()
	meta := Meta{
		ID:          opt.ID,
		Name:        opt.Name,
		Icon:        opt.Icon,
		Description: opt.Description,
		CustomPath:  opt.CustomPath,
		Defaults:    opt.Defaults,
		Tags:        opt.Tags,
		OwnerUserID: opt.OwnerUserID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := storage.WriteJSON(layout.ProjectMeta(opt.ID), &meta); err != nil {
		_ = os.RemoveAll(dir)
		return Project{}, err
	}
	if opt.CustomPath == "" {
		if err := os.MkdirAll(layout.ProjectManagedPath(opt.ID), 0o755); err != nil {
			_ = os.RemoveAll(dir)
			return Project{}, err
		}
	}
	return Project{Meta: meta}, nil
}

// Load reads projects/<id>/meta.json.
func Load(layout config.Layout, id string) (Project, error) {
	if id == "" {
		return Project{}, fmt.Errorf("project id is empty")
	}
	var meta Meta
	if err := storage.ReadJSON(layout.ProjectMeta(id), &meta); err != nil {
		return Project{}, err
	}
	return Project{Meta: meta}, nil
}

// SaveMeta atomically rewrites projects/<id>/meta.json and bumps UpdatedAt.
func SaveMeta(layout config.Layout, id string, meta Meta) error {
	if id == "" {
		return fmt.Errorf("project id is empty")
	}
	if !storage.PathExists(layout.ProjectDir(id)) {
		return fmt.Errorf("project %q not found", id)
	}
	meta.UpdatedAt = time.Now().UTC()
	return storage.WriteJSON(layout.ProjectMeta(id), &meta)
}

// List returns every project ID found on disk (sorted).
func List(layout config.Layout) ([]string, error) {
	return storage.ScanDirNames(layout.ProjectsDir())
}

// RewriteProvider re-points every project whose Defaults.Provider equals
// oldKey to newKey, persisting each change. Used when a provider
// instance is renamed so project defaults follow the new "type/name"
// automatically (live sessions are intentionally left alone — they keep
// the old key and must be re-pointed by the user). Returns the number
// of projects updated. A best-effort op: a single project's load/save
// failure is logged via the returned error only if EVERY candidate
// failed; partial success still returns the count of those that saved.
func RewriteProvider(layout config.Layout, oldKey, newKey string) (int, error) {
	if oldKey == "" || oldKey == newKey {
		return 0, nil
	}
	ids, err := List(layout)
	if err != nil {
		return 0, err
	}
	updated := 0
	var lastErr error
	for _, id := range ids {
		p, err := Load(layout, id)
		if err != nil {
			lastErr = err
			continue
		}
		if p.Meta.Defaults.Provider != oldKey {
			continue
		}
		p.Meta.Defaults.Provider = newKey
		if err := SaveMeta(layout, id, p.Meta); err != nil {
			lastErr = err
			continue
		}
		updated++
	}
	if updated == 0 && lastErr != nil {
		return 0, lastErr
	}
	return updated, nil
}

// Access carries the caller identity used to filter project visibility.
// Build it in the handler from the request context so this package keeps
// no login/http imports:
//
//	project.Access{
//	    UserID:  user.ID,
//	    TagIDs:  login.GetUserTagIDs(ctx),
//	    IsAdmin: user.IsAdmin(),
//	}
type Access struct {
	UserID  string
	TagIDs  []string
	IsAdmin bool
}

// CanAccess reports whether meta is visible to acc. Mirrors the
// Service.CanAccessTool rule (admin bypass, untagged = open, otherwise tag
// match) and adds the project owner rule. Checked in order:
//
//   - admins/owners see every project
//   - a project with no owner and no tags is shared (everyone)
//   - the owner sees their own project
//   - a user carrying any of the project's tags sees it (tag share)
func CanAccess(meta Meta, acc Access) bool {
	if acc.IsAdmin {
		return true
	}
	if meta.OwnerUserID == "" && len(meta.Tags) == 0 {
		return true
	}
	if meta.OwnerUserID != "" && meta.OwnerUserID == acc.UserID {
		return true
	}
	for _, pt := range meta.Tags {
		for _, ut := range acc.TagIDs {
			if pt == ut {
				return true
			}
		}
	}
	return false
}

// ListVisibleTo returns the IDs of projects acc may see, sorted like List.
// Use this for every user-facing project enumeration (channel default
// dropdown, pickers) instead of List, which returns all projects on disk.
func ListVisibleTo(layout config.Layout, acc Access) ([]string, error) {
	ids, err := List(layout)
	if err != nil {
		return nil, err
	}
	if acc.IsAdmin {
		return ids, nil
	}
	out := ids[:0:len(ids)]
	for _, id := range ids {
		var m Meta
		if rerr := storage.ReadJSON(layout.ProjectMeta(id), &m); rerr != nil {
			continue
		}
		if CanAccess(m, acc) {
			out = append(out, id)
		}
	}
	return out, nil
}

// Exists reports whether a project with the given id exists on disk.
func Exists(layout config.Layout, id string) bool {
	if id == "" {
		return false
	}
	return storage.PathExists(layout.ProjectMeta(id))
}

// Delete removes the project metadata folder.
// For managed projects: also removes projects/<id>/ (including files/).
// For custom projects: the external folder is NOT touched.
//
// Two projects are protected from deletion:
//   - the built-in "default" project (matched by name)
//   - any personal project (one carrying the PersonalTag) — the project
//     auto-created for a user is their permanent default and cannot be
//     removed. Use IsProtected to check this without attempting a delete.
func Delete(layout config.Layout, id string) error {
	if id == "" {
		return fmt.Errorf("project id is empty")
	}
	p, err := Load(layout, id)
	if err != nil {
		return err
	}
	if IsProtected(p.Meta) {
		return fmt.Errorf("the default project cannot be deleted")
	}
	return os.RemoveAll(layout.ProjectDir(id))
}

// PersonalTag marks the project auto-created for a user as their
// permanent default. It's the explicit, self-documenting flag used by
// IsProtected to keep the project undeletable — clearer than inferring
// "personal" from the owner field or the 👤 icon (which a user could
// change).
const PersonalTag = "personal"

// IsProtected reports whether meta names a project that cannot be
// deleted: the built-in "default" project (matched by name), or a
// personal project (one carrying PersonalTag — the auto-created per-user
// default). The UI uses this to hide/disable the delete control.
func IsProtected(meta Meta) bool {
	return meta.Name == DefaultName || slices.Contains(meta.Tags, PersonalTag)
}

// ResolvePath returns the cwd for agent subprocesses bound to this project.
// Custom paths win; managed falls back to projects/<id>/files/.
func ResolvePath(layout config.Layout, id string) (string, error) {
	p, err := Load(layout, id)
	if err != nil {
		return "", err
	}
	if p.Meta.CustomPath != "" {
		return p.Meta.CustomPath, nil
	}
	return layout.ProjectManagedPath(id), nil
}

// DefaultName is the name of the built-in project that ships with every
// fresh install.
const DefaultName = "default"

// EnsureDefault creates the "default" project if no projects exist yet.
// Called from Bootstrap after migration so fresh installs always have a
// usable project.
func EnsureDefault(layout config.Layout, newID func() string) error {
	ids, err := List(layout)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		// At least one project exists (post-migration or fresh install that already ran).
		return nil
	}
	_, err = Create(layout, CreateOptions{
		ID:          newID(),
		Name:        DefaultName,
		Icon:        "📁",
		Description: "Built-in default project.",
	})
	return err
}

// FindPersonalProject returns the project ID owned by userID, or "" if none exists.
// It scans all projects on disk looking for a matching OwnerUserID field.
func FindPersonalProject(layout config.Layout, userID string) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("userID is required")
	}
	ids, err := List(layout)
	if err != nil {
		return "", fmt.Errorf("FindPersonalProject list: %w", err)
	}
	for _, id := range ids {
		var m Meta
		if rerr := storage.ReadJSON(layout.ProjectMeta(id), &m); rerr != nil {
			continue
		}
		if m.OwnerUserID == userID {
			return m.ID, nil
		}
	}
	return "", nil
}

// PersonalProjectOptions returns CreateOptions pre-filled for a personal project
// owned by userID. The caller is responsible for generating a unique ID and
// calling Create (or the registry manager's CreateProject) with the result.
func PersonalProjectOptions(newID, userID, displayName string) CreateOptions {
	name := displayName
	if name == "" {
		name = "Personal"
	}
	return CreateOptions{
		ID:          newID,
		Name:        name,
		Icon:        "👤",
		Description: "Personal project.",
		Tags:        []string{PersonalTag},
		OwnerUserID: userID,
	}
}
