// Package service is the CRUD facade over `<BaseDir>/workflows/`. All
// file IO goes through Service so callers (engine, MCP, canvas, UI)
// can swap implementations (in-memory test fakes, audited wrappers).
package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/env"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
)

// ErrNotFound is returned when an id is missing.
var ErrNotFound = errors.New("workflow not found")

// Service is the CRUD contract.
type Service interface {
	List() ([]string, error)
	Load(id string) (workflow.Workflow, error)
	Create(id string, w workflow.Workflow, files map[string][]byte) error
	Update(id string, w workflow.Workflow, files map[string][]byte) error
	Delete(id string) error
	Toggle(id string, enabled bool) error

	// Draft/Publish lifecycle.
	// LoadDraft returns the draft if present, else the published workflow.
	// HasDraft reports whether workflow.draft.yaml exists.
	// SaveDraft writes the canvas state to workflow.draft.yaml.
	// Publish promotes the draft to workflow.yaml and removes the draft.
	// DiscardDraft removes workflow.draft.yaml (revert to published).
	LoadDraft(id string) (workflow.Workflow, error)
	HasDraft(id string) bool
	SaveDraft(id string, w workflow.Workflow) error
	Publish(id string) (workflow.Workflow, error)
	DiscardDraft(id string) error

	ListFiles(id string) ([]string, error)
	ReadFile(id, relPath string) ([]byte, error)
	WriteFile(id, relPath string, data []byte) error
	DeleteFile(id, relPath string) error

	LoadState(id string) (workflow.WorkflowState, error)
	SaveState(id string, st workflow.WorkflowState) error

	LoadEnvValues(id string) (map[string]string, error)
	SaveEnvValues(id string, values map[string]string) error

	BaseDir() string
}

// FileService is the on-disk implementation.
type FileService struct {
	Layout config.Layout
}

// New constructs a FileService.
func New(layout config.Layout) *FileService {
	return &FileService{Layout: layout}
}

// BaseDir returns the workflows root for diagnostics + MCP exposure.
func (s *FileService) BaseDir() string { return s.Layout.WorkflowsDir() }

// List returns every workflow id, sorted.
func (s *FileService) List() ([]string, error) {
	return storage.ScanDirNames(s.Layout.WorkflowsDir())
}

// Load reads + parses a workflow.yaml.
func (s *FileService) Load(id string) (workflow.Workflow, error) {
	if err := parse.ValidateID(id); err != nil {
		return workflow.Workflow{}, err
	}
	path := s.Layout.WorkflowFile(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return workflow.Workflow{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return workflow.Workflow{}, err
	}
	w, err := parse.Parse(id, data)
	if err != nil {
		return workflow.Workflow{}, err
	}
	return w, nil
}

// Create scaffolds a new folder.
func (s *FileService) Create(id string, w workflow.Workflow, files map[string][]byte) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	dir := s.Layout.WorkflowDir(id)
	if storage.PathExists(dir) {
		return fmt.Errorf("workflow %q already exists", id)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "nodes"), 0o755); err != nil {
		return err
	}
	w.ID = id
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}
	if err := s.writeWorkflowYAML(id, w); err != nil {
		return err
	}
	for rel, data := range files {
		if err := s.WriteFile(id, rel, data); err != nil {
			return err
		}
	}
	return nil
}

// Update overwrites workflow.yaml + optional supporting files.
func (s *FileService) Update(id string, w workflow.Workflow, files map[string][]byte) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	if !storage.PathExists(s.Layout.WorkflowDir(id)) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	w.ID = id
	if err := s.writeWorkflowYAML(id, w); err != nil {
		return err
	}
	for rel, data := range files {
		if err := s.WriteFile(id, rel, data); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes the folder.
func (s *FileService) Delete(id string) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	dir := s.Layout.WorkflowDir(id)
	if !storage.PathExists(dir) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return os.RemoveAll(dir)
}

// Toggle flips enabled flag atomically.
func (s *FileService) Toggle(id string, enabled bool) error {
	w, err := s.Load(id)
	if err != nil {
		return err
	}
	w.Enabled = enabled
	return s.writeWorkflowYAML(id, w)
}

// ListFiles walks the workflow folder excluding runs/.
func (s *FileService) ListFiles(id string) ([]string, error) {
	if err := parse.ValidateID(id); err != nil {
		return nil, err
	}
	root := s.Layout.WorkflowDir(id)
	if !storage.PathExists(root) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	out := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if rel == "runs" || strings.HasPrefix(rel, "runs/") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReadFile reads a relative path inside the workflow folder.
func (s *FileService) ReadFile(id, relPath string) ([]byte, error) {
	abs, err := s.safePath(id, relPath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

// WriteFile writes a relative path atomically.
func (s *FileService) WriteFile(id, relPath string, data []byte) error {
	abs, err := s.safePath(id, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return WriteAtomic(abs, data)
}

// DeleteFile removes a relative path inside the workflow folder.
func (s *FileService) DeleteFile(id, relPath string) error {
	abs, err := s.safePath(id, relPath)
	if err != nil {
		return err
	}
	return os.Remove(abs)
}

// LoadState reads `<id>/state.json`. Missing file returns zero value.
func (s *FileService) LoadState(id string) (workflow.WorkflowState, error) {
	var st workflow.WorkflowState
	err := storage.ReadJSON(s.Layout.WorkflowStateFile(id), &st)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return workflow.WorkflowState{}, nil
		}
		return workflow.WorkflowState{}, err
	}
	return st, nil
}

// SaveState writes `<id>/state.json` atomically.
func (s *FileService) SaveState(id string, st workflow.WorkflowState) error {
	return storage.WriteJSON(s.Layout.WorkflowStateFile(id), st)
}

// LoadEnvValues reads `<id>/env.yaml`.
func (s *FileService) LoadEnvValues(id string) (map[string]string, error) {
	out := map[string]string{}
	data, err := os.ReadFile(s.Layout.WorkflowEnvFile(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	if err := env.UnmarshalYAMLFile(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SaveEnvValues writes `<id>/env.yaml` atomically.
func (s *FileService) SaveEnvValues(id string, values map[string]string) error {
	data, err := env.MarshalYAMLFile(values)
	if err != nil {
		return err
	}
	return WriteAtomic(s.Layout.WorkflowEnvFile(id), data)
}

func (s *FileService) writeWorkflowYAML(id string, w workflow.Workflow) error {
	data, err := parse.Marshal(w)
	if err != nil {
		return err
	}
	return WriteAtomic(s.Layout.WorkflowFile(id), data)
}

// ── Draft / Publish lifecycle ────────────────────────────────────────

// HasDraft reports whether a workflow.draft.yaml file exists.
func (s *FileService) HasDraft(id string) bool {
	if err := parse.ValidateID(id); err != nil {
		return false
	}
	_, err := os.Stat(s.Layout.WorkflowDraftFile(id))
	return err == nil
}

// LoadDraft loads the draft file if present, otherwise falls back to
// the published workflow. Editor always opens this so the user sees
// their in-progress edits across refreshes.
func (s *FileService) LoadDraft(id string) (workflow.Workflow, error) {
	if err := parse.ValidateID(id); err != nil {
		return workflow.Workflow{}, err
	}
	draftPath := s.Layout.WorkflowDraftFile(id)
	if data, err := os.ReadFile(draftPath); err == nil {
		w, perr := parse.Parse(id, data)
		if perr != nil {
			return workflow.Workflow{}, perr
		}
		return w, nil
	}
	return s.Load(id)
}

// SaveDraft writes the workflow to workflow.draft.yaml. Never touches
// workflow.yaml — Publish is the only path that promotes a draft to
// live. Carries forward the published ID + CreatedAt when draft is
// blank on those fields.
func (s *FileService) SaveDraft(id string, w workflow.Workflow) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	if !storage.PathExists(s.Layout.WorkflowDir(id)) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	w.ID = id
	if w.CreatedAt.IsZero() {
		if prev, err := s.Load(id); err == nil && !prev.CreatedAt.IsZero() {
			w.CreatedAt = prev.CreatedAt
		} else {
			w.CreatedAt = time.Now().UTC()
		}
	}
	data, err := parse.Marshal(w)
	if err != nil {
		return err
	}
	return WriteAtomic(s.Layout.WorkflowDraftFile(id), data)
}

// Publish promotes the draft to workflow.yaml and removes the draft.
// Returns the published workflow. No-op (returns current published)
// when no draft exists.
func (s *FileService) Publish(id string) (workflow.Workflow, error) {
	if err := parse.ValidateID(id); err != nil {
		return workflow.Workflow{}, err
	}
	draftPath := s.Layout.WorkflowDraftFile(id)
	data, err := os.ReadFile(draftPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.Load(id)
		}
		return workflow.Workflow{}, err
	}
	w, err := parse.Parse(id, data)
	if err != nil {
		return workflow.Workflow{}, fmt.Errorf("draft parse: %w", err)
	}
	if err := WriteAtomic(s.Layout.WorkflowFile(id), data); err != nil {
		return workflow.Workflow{}, err
	}
	if err := os.Remove(draftPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return w, err
	}
	return w, nil
}

// DiscardDraft removes the draft file. No-op if no draft.
func (s *FileService) DiscardDraft(id string) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	err := os.Remove(s.Layout.WorkflowDraftFile(id))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *FileService) safePath(id, relPath string) (string, error) {
	if err := parse.ValidateID(id); err != nil {
		return "", err
	}
	if relPath == "" {
		return "", fmt.Errorf("relPath is empty")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("absolute path not allowed: %s", relPath)
	}
	clean := filepath.Clean(relPath)
	if strings.HasPrefix(clean, "..") || strings.Contains(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal not allowed: %s", relPath)
	}
	if strings.HasPrefix(clean, string(filepath.Separator)) {
		return "", fmt.Errorf("absolute path not allowed: %s", relPath)
	}
	root := s.Layout.WorkflowDir(id)
	abs := filepath.Join(root, clean)
	absResolved, err := filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absResolved, rootAbs) {
		return "", fmt.Errorf("path escapes workflow folder: %s", relPath)
	}
	return abs, nil
}

// WriteAtomic does tmp+rename so a crash leaves the old file intact.
// Exported because the engine + canvas use it too.
func WriteAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
