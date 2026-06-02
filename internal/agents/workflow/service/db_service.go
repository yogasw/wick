package service

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/agents/workflow/repository"
)

// DBService is the database-primary implementation of Service. The
// workflow definition (body + draft + version history + supporting
// files + test fixtures) lives in SQL via repository.Repo; runtime
// concerns the engine needs from the filesystem (state.json, env.yaml,
// runs/<id>/) still go through the embedded FileService.
//
// Composition over inheritance: FileService is embedded so methods we
// don't override (LoadState/SaveState, LoadEnvValues/SaveEnvValues,
// BaseDir) keep their on-disk semantics. The runtime side of wick
// continues to write state + events as JSON/JSONL files so the engine
// stays cheap and crash-friendly.
type DBService struct {
	*FileService
	repo *repository.Repo
}

// NewDB constructs a DBService with the shared Repo and Layout. The
// Layout is still required because the embedded FileService owns the
// state.json + env.yaml + runs/<id>/ paths.
func NewDB(layout config.Layout, repo *repository.Repo) *DBService {
	return &DBService{
		FileService: New(layout),
		repo:        repo,
	}
}

// List returns every workflow id stored in the DB, ordered by id so
// the SPA list stays deterministic across reloads. The DB-backed list
// does NOT scan disk — folders left over from the file era are
// invisible to the SPA once the boot importer has either migrated them
// or skipped them as unreadable.
func (s *DBService) List() ([]string, error) {
	rows, err := s.repo.List()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out, nil
}

// Load returns the published workflow. Falls back to the draft when
// nothing has been published yet — matches the file-based Service.Load
// semantics so the engine keeps booting from whichever copy exists.
func (s *DBService) Load(id string) (workflow.Workflow, error) {
	if err := parse.ValidateID(id); err != nil {
		return workflow.Workflow{}, err
	}
	w, err := s.repo.LoadWorkflow(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return workflow.Workflow{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return workflow.Workflow{}, err
	}
	return w, nil
}

// FindByName resolves a display-name conflict by querying every row in
// the workflows table. Case-insensitive trim match — same contract the
// file-store advertised.
func (s *DBService) FindByName(name, exceptID string) (string, error) {
	target := strings.ToLower(strings.TrimSpace(name))
	if target == "" {
		return "", nil
	}
	rows, err := s.repo.List()
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		if r.ID == exceptID {
			continue
		}
		if strings.ToLower(strings.TrimSpace(r.Name)) == target {
			return r.ID, nil
		}
	}
	return "", nil
}

// Create inserts the workflow row + the initial supporting files. The
// first SaveDraft (from MCP / SPA) appends the first version history
// entry — Create itself doesn't snapshot, matching the file-store
// behaviour where a brand-new folder has no draft yet.
func (s *DBService) Create(id string, w workflow.Workflow, files map[string][]byte) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	if strings.TrimSpace(w.Name) != "" {
		existing, err := s.FindByName(w.Name, "")
		if err != nil {
			return err
		}
		if existing != "" {
			return fmt.Errorf("%w: %q used by workflow %q", ErrNameTaken, w.Name, existing)
		}
	}
	if _, err := s.repo.Get(id); err == nil {
		return fmt.Errorf("workflow %q already exists", id)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err := s.repo.Create(id, w.Name, w.CreatedBy); err != nil {
		return err
	}
	w.ID = id
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}
	// Seed the workflow body as the initial draft so handlers can edit
	// from a known starting point. Files (prompts, scripts, fixtures)
	// land in their own table via the supporting-file path.
	if _, err := s.repo.SaveDraft(id, w, w.CreatedBy, "initial scaffold"); err != nil {
		return err
	}
	for rel, data := range files {
		if err := s.WriteFile(id, rel, data); err != nil {
			return err
		}
	}
	return nil
}

// Update replaces the workflow body in-place + (optionally) supporting
// files. Used by rename / metadata-only paths where the canvas isn't
// driving the change. Internally this writes through the published
// slot (Repo.SaveDraft + Publish would create a phantom draft).
func (s *DBService) Update(id string, w workflow.Workflow, files map[string][]byte) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	if _, err := s.repo.Get(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return err
	}
	w.ID = id
	body, err := parse.Marshal(w)
	if err != nil {
		return err
	}
	if err := s.repo.SetPublished(id, w.Name, w.Enabled, w.Version, body); err != nil {
		return err
	}
	for rel, data := range files {
		if err := s.WriteFile(id, rel, data); err != nil {
			return err
		}
	}
	return nil
}

// Delete drops the workflow row + every cascading table (versions,
// files, test cases) in one transaction. The on-disk runs/ folder is
// removed too so old run logs don't linger.
func (s *DBService) Delete(id string) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	if _, err := s.repo.Get(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return err
	}
	if err := s.repo.Delete(id); err != nil {
		return err
	}
	// Best-effort cleanup of runtime files; ignore not-exist since the
	// folder may never have existed for fresh workflows.
	_ = os.RemoveAll(s.Layout.WorkflowDir(id))
	return nil
}

// Toggle flips the Enabled flag on the published copy and clones the
// change into the draft (when a draft exists) so the SPA editor's
// header chip stays in sync with the live router.
func (s *DBService) Toggle(id string, enabled bool) error {
	w, err := s.LoadDraft(id)
	if err != nil {
		return err
	}
	w.Enabled = enabled
	body, err := parse.Marshal(w)
	if err != nil {
		return err
	}
	return s.repo.SetEnabled(id, enabled, body)
}

// ── Draft lifecycle ─────────────────────────────────────────────────

// LoadDraft returns the draft if one exists, otherwise the published
// copy. ErrNotFound when no row exists at all.
func (s *DBService) LoadDraft(id string) (workflow.Workflow, error) {
	if err := parse.ValidateID(id); err != nil {
		return workflow.Workflow{}, err
	}
	w, err := s.repo.LoadDraft(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return workflow.Workflow{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return workflow.Workflow{}, err
	}
	return w, nil
}

// HasDraft reports whether a draft body is currently persisted for the
// workflow. Falls through to false on lookup error so callers can
// proceed without a 500.
func (s *DBService) HasDraft(id string) bool {
	if err := parse.ValidateID(id); err != nil {
		return false
	}
	row, err := s.repo.Get(id)
	if err != nil {
		return false
	}
	return row.HasDraft
}

// SaveDraft persists the canvas state. Appends a new draft snapshot to
// workflow_versions; retention is enforced by Repo.SaveDraft.
func (s *DBService) SaveDraft(id string, w workflow.Workflow) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	if _, err := s.repo.Get(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return err
	}
	w.ID = id
	if w.CreatedAt.IsZero() {
		if prev, perr := s.repo.LoadDraft(id); perr == nil && !prev.CreatedAt.IsZero() {
			w.CreatedAt = prev.CreatedAt
		} else {
			w.CreatedAt = time.Now().UTC()
		}
	}
	_, err := s.repo.SaveDraft(id, w, w.CreatedBy, "")
	return err
}

// Publish promotes the draft to the published slot, appends a
// published-kind snapshot, and validates before committing so a broken
// draft can't go live.
func (s *DBService) Publish(id string) (workflow.Workflow, error) {
	if err := parse.ValidateID(id); err != nil {
		return workflow.Workflow{}, err
	}
	row, err := s.repo.Get(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return workflow.Workflow{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return workflow.Workflow{}, err
	}
	if !row.HasDraft {
		// Nothing to publish — return current published.
		return s.Load(id)
	}
	draft, err := s.repo.LoadDraft(id)
	if err != nil {
		return workflow.Workflow{}, err
	}
	if r := parse.Validate(draft); !r.Ok() {
		return workflow.Workflow{}, fmt.Errorf("cannot publish — fix validation errors:\n%s", r.Error())
	}
	if _, err := s.repo.Publish(id, draft.CreatedBy, ""); err != nil {
		return workflow.Workflow{}, err
	}
	return draft, nil
}

// DiscardDraft wipes the draft slot and HasDraft flag.
func (s *DBService) DiscardDraft(id string) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	return s.repo.DiscardDraft(id)
}

// ── Test fixtures (__tests__/*.json) ────────────────────────────────

// ListFiles returns every test fixture path stored against the
// workflow. The DB-primary store only persists user-authored data —
// the workflow body lives in its own column, runs/ stay on disk, so
// the only path-addressable surface left is __tests__/ from
// workflow_test_cases.
func (s *DBService) ListFiles(id string) ([]string, error) {
	if err := parse.ValidateID(id); err != nil {
		return nil, err
	}
	if _, err := s.repo.Get(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, err
	}
	rows, err := s.repo.ListTests(id)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = "__tests__/" + r.Name + ".json"
	}
	return out, nil
}

// ReadFile returns the raw bytes for one test fixture under
// __tests__/<name>.json. Any other path errors as not-exist — there is
// no supporting-files concept in the DB-primary store.
func (s *DBService) ReadFile(id, relPath string) ([]byte, error) {
	if err := parse.ValidateID(id); err != nil {
		return nil, err
	}
	name, ok := strings.CutPrefix(relPath, "__tests__/")
	if !ok {
		return nil, fmt.Errorf("%w: %s (only __tests__/*.json is readable in DB store)", os.ErrNotExist, relPath)
	}
	name = strings.TrimSuffix(name, ".json")
	row, err := s.repo.GetTest(id, name)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: %s", os.ErrNotExist, relPath)
		}
		return nil, err
	}
	return []byte(row.Body), nil
}

// WriteFile creates or replaces one test fixture under
// __tests__/<name>.json. Any other path is rejected.
func (s *DBService) WriteFile(id, relPath string, data []byte) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	name, ok := strings.CutPrefix(relPath, "__tests__/")
	if !ok {
		return fmt.Errorf("only __tests__/*.json paths are writable in DB store: %s", relPath)
	}
	name = strings.TrimSuffix(name, ".json")
	return s.repo.SaveTest(id, name, data)
}

// DeleteFile drops one test fixture. No-op-equivalent error for any
// other path — the DB store doesn't model arbitrary files.
func (s *DBService) DeleteFile(id, relPath string) error {
	if err := parse.ValidateID(id); err != nil {
		return err
	}
	name, ok := strings.CutPrefix(relPath, "__tests__/")
	if !ok {
		return fmt.Errorf("only __tests__/*.json paths are deletable in DB store: %s", relPath)
	}
	name = strings.TrimSuffix(name, ".json")
	return s.repo.DeleteTest(id, name)
}
