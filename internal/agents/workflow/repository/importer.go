package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/entity"
)

// FileLister is the narrow slice of workflow.Service this importer
// needs — just enough to walk the file-based store and pull each
// workflow's parsed form. Keeps the dependency one-way; importer
// pulls from FileService, never the reverse.
type FileLister interface {
	List() ([]string, error)
	Load(id string) (wf.Workflow, error)
	HasDraft(id string) bool
	LoadDraft(id string) (wf.Workflow, error)
}

// ImportFromFiles seeds the DB with every workflow on disk that doesn't
// already exist as a row. Idempotent: existing DB rows are left alone
// so subsequent boots don't clobber in-flight edits made through the
// SPA. Each imported workflow gets one "published" snapshot stamped
// "initial import" — that gives the History tab a non-empty anchor
// from day one.
//
// Returns the number of workflows imported (zero on a re-run).
func (r *Repo) ImportFromFiles(svc FileLister) (int, error) {
	ids, err := svc.List()
	if err != nil {
		return 0, err
	}
	var imported int
	for _, id := range ids {
		// Skip workflows already in the DB — don't overwrite.
		var existing entity.Workflow
		if err := r.db.Where("id = ?", id).First(&existing).Error; err == nil {
			continue
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return imported, err
		}

		w, err := svc.Load(id)
		if err != nil {
			// Could be a folder with no valid workflow — log + skip.
			continue
		}
		bodyBytes, err := parse.Marshal(w)
		if err != nil {
			return imported, err
		}

		// Seed the workflow + a published anchor. Importer treats the
		// existing file YAML as "published" because that's what users
		// see in prod today; drafts are layered on top.
		row := entity.Workflow{
			ID:            id,
			Name:          w.Name,
			Enabled:       w.Enabled,
			Version:       w.Version,
			BodyPublished: string(bodyBytes),
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		// Layer in a draft if the file store has one.
		if svc.HasDraft(id) {
			if dw, err := svc.LoadDraft(id); err == nil {
				if draftBytes, err := parse.Marshal(dw); err == nil {
					row.BodyDraft = string(draftBytes)
					row.HasDraft = true
				}
			}
		}

		err = r.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			snap := entity.WorkflowVersion{
				WorkflowID: id,
				Kind:       KindPublished,
				Body:       row.BodyPublished,
				Message:    "initial import",
				CreatedAt:  row.CreatedAt,
			}
			if err := tx.Create(&snap).Error; err != nil {
				return err
			}
			if row.HasDraft {
				draftSnap := entity.WorkflowVersion{
					WorkflowID: id,
					Kind:       KindDraft,
					Body:       row.BodyDraft,
					Message:    "initial import — draft",
					CreatedAt:  row.CreatedAt,
				}
				if err := tx.Create(&draftSnap).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}
