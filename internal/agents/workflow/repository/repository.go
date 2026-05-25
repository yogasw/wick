// Package repository is the DB-backed storage for workflows + their
// version history. Sits behind the workflow.Service interface so the
// caller — manager bootstrap, handlers, MCP — doesn't care whether the
// underlying store is the legacy file tree or the new SQL tables.
//
// The migration policy lives in svelte-migration.md: tables ride
// alongside the file store until Phase DB-3 flips primary writes here;
// during the parallel window, every successful file write is mirrored
// here as a snapshot so the history UI has data to render.
package repository

import (
	"errors"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/entity"
)

// Kind constants for WorkflowVersion.Kind. Strings (not iota) so the
// DB rows stay self-describing for ad-hoc queries.
const (
	KindDraft     = "draft"
	KindPublished = "published"
)

// DraftRetention is the default number of draft snapshots kept per
// workflow. Drafts churn on every keystroke-triggered save; published
// rows are retained indefinitely. Tunable later via a Config row.
const DraftRetention = 50

// Repo owns CRUD + version history for workflows. Methods are
// pointer-receiver so callers can swap a fake in tests.
type Repo struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Repo { return &Repo{db: db} }

// List returns every workflow ordered by updated-at desc — newest
// edits first. Used by the SPA workflow list.
func (r *Repo) List() ([]entity.Workflow, error) {
	var rows []entity.Workflow
	err := r.db.Order("updated_at desc").Find(&rows).Error
	return rows, err
}

// Get loads one workflow row by id.
func (r *Repo) Get(id string) (entity.Workflow, error) {
	var row entity.Workflow
	err := r.db.Where("id = ?", id).First(&row).Error
	return row, err
}

// LoadWorkflow returns the parsed published workflow. Falls back to
// the draft when nothing has been published yet — same policy as the
// file-based Service.Load.
func (r *Repo) LoadWorkflow(id string) (wf.Workflow, error) {
	row, err := r.Get(id)
	if err != nil {
		return wf.Workflow{}, err
	}
	yamlText := row.YAMLPublished
	if yamlText == "" {
		yamlText = row.YAMLDraft
	}
	if yamlText == "" {
		return wf.Workflow{}, errors.New("workflow has no yaml")
	}
	return parse.Parse(id, []byte(yamlText))
}

// LoadDraft returns the parsed draft when one exists, otherwise the
// published copy. Matches FileService.LoadDraft semantics.
func (r *Repo) LoadDraft(id string) (wf.Workflow, error) {
	row, err := r.Get(id)
	if err != nil {
		return wf.Workflow{}, err
	}
	yamlText := row.YAMLDraft
	if yamlText == "" {
		yamlText = row.YAMLPublished
	}
	if yamlText == "" {
		return wf.Workflow{}, errors.New("workflow has no yaml")
	}
	return parse.Parse(id, []byte(yamlText))
}

// SaveDraft persists the workflow as the active draft and appends a
// new draft snapshot to workflow_versions. Returns the snapshot id so
// the caller can surface "saved as v42" in the UI.
//
// Side effect: enforces DraftRetention by deleting the oldest excess
// draft rows for this workflow. Published rows are never pruned.
func (r *Repo) SaveDraft(id string, w wf.Workflow, createdBy, message string) (uint, error) {
	yamlBytes, err := yaml.Marshal(w)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	var version uint
	err = r.db.Transaction(func(tx *gorm.DB) error {
		// Update only the draft-relevant columns so YAMLPublished is
		// preserved across edits. Insert when the row doesn't exist
		// yet (covers paths that skip Create — e.g. importer + tests).
		var existing entity.Workflow
		if err := tx.Where("id = ?", id).First(&existing).Error; err != nil {
			if err != gorm.ErrRecordNotFound {
				return err
			}
			existing = entity.Workflow{ID: id, CreatedAt: now}
		}
		existing.Name = w.Name
		existing.Enabled = w.Enabled
		existing.Version = w.Version
		existing.YAMLDraft = string(yamlBytes)
		existing.HasDraft = true
		existing.UpdatedAt = now
		if err := tx.Save(&existing).Error; err != nil {
			return err
		}
		snap := entity.WorkflowVersion{
			WorkflowID: id,
			Kind:       KindDraft,
			YAML:       string(yamlBytes),
			Message:    message,
			CreatedBy:  createdBy,
			CreatedAt:  now,
		}
		if err := tx.Create(&snap).Error; err != nil {
			return err
		}
		version = snap.ID
		return r.pruneDrafts(tx, id, DraftRetention)
	})
	return version, err
}

// Publish promotes the current draft to published. The published yaml
// becomes the new YAMLPublished column and a snapshot is appended to
// workflow_versions with Kind=published. The draft column is cleared
// and HasDraft flipped to false so the next save creates a fresh
// draft, matching the file-based publish flow.
func (r *Repo) Publish(id, createdBy, message string) (uint, error) {
	var snapID uint
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var row entity.Workflow
		if err := tx.Where("id = ?", id).First(&row).Error; err != nil {
			return err
		}
		if row.YAMLDraft == "" {
			return errors.New("no draft to publish")
		}
		now := time.Now()
		row.YAMLPublished = row.YAMLDraft
		row.YAMLDraft = ""
		row.HasDraft = false
		row.UpdatedAt = now
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		snap := entity.WorkflowVersion{
			WorkflowID: id,
			Kind:       KindPublished,
			YAML:       row.YAMLPublished,
			Message:    message,
			CreatedBy:  createdBy,
			CreatedAt:  now,
		}
		if err := tx.Create(&snap).Error; err != nil {
			return err
		}
		snapID = snap.ID
		return nil
	})
	return snapID, err
}

// DiscardDraft clears the draft and HasDraft flag without touching the
// published copy or the version history. Use when the user explicitly
// aborts an edit.
func (r *Repo) DiscardDraft(id string) error {
	return r.db.Model(&entity.Workflow{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"yaml_draft": "",
			"has_draft":  false,
			"updated_at": time.Now(),
		}).Error
}

// Create inserts a brand-new workflow row. Used by the importer
// (file → DB) and the SPA create flow. Workflows start disabled +
// published-empty; the first SaveDraft creates the initial version.
func (r *Repo) Create(id, name, createdBy string) error {
	row := entity.Workflow{
		ID:        id,
		Name:      name,
		Enabled:   false,
		CreatedBy: createdBy,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return r.db.Create(&row).Error
}

// Delete removes the workflow row + every version snapshot + every
// test case. Matches the file-store semantics where deleting the
// folder wipes everything.
func (r *Repo) Delete(id string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("workflow_id = ?", id).Delete(&entity.WorkflowVersion{}).Error; err != nil {
			return err
		}
		if err := tx.Where("workflow_id = ?", id).Delete(&entity.WorkflowTestCase{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&entity.Workflow{}).Error
	})
}

// Versions returns the version history for a workflow ordered newest
// first. Drives the History tab in the SPA.
func (r *Repo) Versions(id string) ([]entity.WorkflowVersion, error) {
	var rows []entity.WorkflowVersion
	err := r.db.Where("workflow_id = ?", id).
		Order("id desc").
		Find(&rows).Error
	return rows, err
}

// Version returns one snapshot by id. Caller asserts ownership against
// the workflowID it expected — the DB row carries it for cross-check.
func (r *Repo) Version(versionID uint) (entity.WorkflowVersion, error) {
	var row entity.WorkflowVersion
	err := r.db.Where("id = ?", versionID).First(&row).Error
	return row, err
}

// Restore copies the YAML from a historic snapshot into the draft
// slot. Doesn't auto-publish — the user must hit Publish to make it
// live. Returns the new draft snapshot id.
func (r *Repo) Restore(id string, versionID uint, createdBy string) (uint, error) {
	var snap entity.WorkflowVersion
	if err := r.db.Where("id = ?", versionID).First(&snap).Error; err != nil {
		return 0, err
	}
	if snap.WorkflowID != id {
		return 0, errors.New("version does not belong to this workflow")
	}
	w, err := parse.Parse(id, []byte(snap.YAML))
	if err != nil {
		return 0, err
	}
	return r.SaveDraft(id, w, createdBy, "restored from v"+itoa(versionID))
}

// pruneDrafts deletes old draft snapshots beyond the retention cap.
// Published rows are excluded — they accumulate freely as the audit
// trail. Returns an error if the prune query fails; soft-fail at the
// caller would be appropriate but the contract here surfaces the raw
// error so tests can assert it.
func (r *Repo) pruneDrafts(tx *gorm.DB, workflowID string, keep int) error {
	// Subquery: ids of the most recent `keep` drafts for this workflow.
	// Delete everything that's both KindDraft AND not in that set.
	subQuery := tx.Model(&entity.WorkflowVersion{}).
		Select("id").
		Where("workflow_id = ? AND kind = ?", workflowID, KindDraft).
		Order("id desc").
		Limit(keep)
	return tx.
		Where("workflow_id = ? AND kind = ? AND id NOT IN (?)", workflowID, KindDraft, subQuery).
		Delete(&entity.WorkflowVersion{}).Error
}

// itoa avoids importing strconv just for one call in Restore.
func itoa(n uint) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
