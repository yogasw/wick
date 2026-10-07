package service

import (
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/repository"
	"github.com/yogasw/wick/internal/entity"
)

// ownerOf reports the owner as every surface sees it: the row column (admin
// page), the published body and the draft body (everything that Loads).
func ownerOf(t *testing.T, svc *DBService, db *gorm.DB, id string) (row, live, draft string) {
	t.Helper()
	var r entity.Workflow
	if err := db.Where("id = ?", id).First(&r).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	pub, err := svc.Load(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	d, err := svc.LoadDraft(id)
	if err != nil {
		t.Fatalf("load draft: %v", err)
	}
	return r.CreatedBy, pub.CreatedBy, d.CreatedBy
}

// TestDBService_EditorNeverBecomesOwner: whoever creates a workflow owns it.
// Saving the canvas used to stamp the saver into the draft body, and Publish
// carried that body live — so the last person to save quietly took the
// workflow over even though the row column still named the creator.
func TestDBService_EditorNeverBecomesOwner(t *testing.T) {
	svc, db := newDBSvc(t)
	id := "owned-by-a"
	w := sampleWF(id)
	w.CreatedBy = "u-a"
	if err := svc.Create(id, w); err != nil {
		t.Fatalf("create: %v", err)
	}

	edit, err := svc.LoadDraft(id)
	if err != nil {
		t.Fatalf("load draft: %v", err)
	}
	edit.Name = "edited by b"
	if err := svc.SaveDraftAs(id, edit, "u-b"); err != nil {
		t.Fatalf("save draft as b: %v", err)
	}
	// A body that names the editor outright (the old canvas behaviour)
	// must not win either.
	edit.CreatedBy = "u-b"
	edit.Name = "edited by b again"
	if err := svc.SaveDraft(id, edit); err != nil {
		t.Fatalf("save draft with b in body: %v", err)
	}
	if _, err := svc.Publish(id, "u-b"); err != nil {
		t.Fatalf("publish as b: %v", err)
	}

	edit.Name = "after publish"
	if err := svc.SaveDraftAs(id, edit, "u-b"); err != nil {
		t.Fatalf("save draft after publish: %v", err)
	}
	if row, live, draft := ownerOf(t, svc, db, id); row != "u-a" || live != "u-a" || draft != "u-a" {
		t.Fatalf("owner row/live/draft = %q/%q/%q, want u-a everywhere", row, live, draft)
	}

	// The editor is still credited where "who did this" belongs.
	var snap entity.WorkflowVersion
	if err := db.Where("workflow_id = ? AND kind = ?", id, "draft").
		Order("id desc").First(&snap).Error; err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if snap.CreatedBy != "u-b" {
		t.Fatalf("draft snapshot author = %q, want the editor u-b", snap.CreatedBy)
	}

	// The admin transfer is the one way ownership moves.
	if err := svc.SetOwner(id, "u-c"); err != nil {
		t.Fatalf("set owner: %v", err)
	}
	if row, live, draft := ownerOf(t, svc, db, id); row != "u-c" || live != "u-c" || draft != "u-c" {
		t.Fatalf("after SetOwner row/live/draft = %q/%q/%q, want u-c everywhere", row, live, draft)
	}
	if err := svc.SaveDraftAs(id, edit, "u-b"); err != nil {
		t.Fatalf("save draft after transfer: %v", err)
	}
	if _, err := svc.Publish(id, "u-b"); err != nil {
		t.Fatalf("publish after transfer: %v", err)
	}
	if row, live, draft := ownerOf(t, svc, db, id); row != "u-c" || live != "u-c" || draft != "u-c" {
		t.Fatalf("transfer undone by an edit: row/live/draft = %q/%q/%q, want u-c", row, live, draft)
	}
}

// TestDBService_DriftedBodyReadsTheRowOwner: rows saved before the fix carry
// the last editor in the body. The column wins on read, so they come out
// right without a migration.
func TestDBService_DriftedBodyReadsTheRowOwner(t *testing.T) {
	svc, db := newDBSvc(t)
	id := "drifted"
	w := sampleWF(id)
	w.CreatedBy = "u-a"
	if err := svc.Create(id, w); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Publish(id, ""); err != nil {
		t.Fatalf("publish: %v", err)
	}
	var row entity.Workflow
	if err := db.Where("id = ?", id).First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	drifted := strings.Replace(row.BodyPublished, `"u-a"`, `"u-b"`, 1)
	if drifted == row.BodyPublished {
		t.Fatal("fixture: owner not found in published body")
	}
	if err := db.Model(&entity.Workflow{}).Where("id = ?", id).
		Updates(map[string]any{"body_published": drifted, "body_draft": drifted, "has_draft": true}).Error; err != nil {
		t.Fatalf("drift: %v", err)
	}
	if _, live, draft := ownerOf(t, svc, db, id); live != "u-a" || draft != "u-a" {
		t.Fatalf("drifted body read as live/draft = %q/%q, want the row owner u-a", live, draft)
	}
	if _, err := svc.Publish(id, "u-b"); err != nil {
		t.Fatalf("publish drifted draft: %v", err)
	}
	if err := db.Where("id = ?", id).First(&row).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if strings.Contains(row.BodyPublished, `"u-b"`) {
		t.Fatal("publish carried the drifted editor into the published body")
	}
}

// TestDBService_PinnedSessionJudgesTheEditor: the pinned-session gate is about
// what the person saving may reach, so it must judge the editor — not the
// owner, whose sessions the editor could otherwise borrow.
func TestDBService_PinnedSessionJudgesTheEditor(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.Workflow{}, &entity.WorkflowVersion{}, &entity.WorkflowTestCase{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	layout := config.NewLayout(filepath.Join(t.TempDir(), "base"))
	if err := layout.EnsureLayout(); err != nil {
		t.Fatalf("layout: %v", err)
	}
	repo := repository.New(db).WithSessionAccess(func(userID, sessionID string) bool {
		return userID == "u-a" && sessionID == "a-session"
	})
	svc := NewDB(layout, repo)

	id := "pinned"
	w := sampleWF(id)
	w.CreatedBy = "u-a"
	if err := svc.Create(id, w); err != nil {
		t.Fatalf("create: %v", err)
	}
	pinned := w
	pinned.Graph.Nodes = append(pinned.Graph.Nodes, workflow.Node{ID: "s", Type: workflow.NodeSessionInit, SessionID: "a-session"})

	if err := svc.SaveDraftAs(id, pinned, "u-b"); err == nil {
		t.Fatal("editor u-b pinned the owner's session it cannot reach")
	}
	if err := svc.SaveDraftAs(id, pinned, "u-a"); err != nil {
		t.Fatalf("owner refused its own session: %v", err)
	}
}

// TestFileService_EditorNeverBecomesOwner: same rule in file mode, where the
// published file stands in for the row.
func TestFileService_EditorNeverBecomesOwner(t *testing.T) {
	svc, _ := newFileSvc(t)
	id := "file-owned"
	w := sampleWF(id)
	w.CreatedBy = "u-a"
	if err := svc.Create(id, w); err != nil {
		t.Fatalf("create: %v", err)
	}
	edit := w
	edit.CreatedBy = "u-b"
	edit.Name = "edited by b"
	if err := svc.SaveDraftAs(id, edit, "u-b"); err != nil {
		t.Fatalf("save draft as b: %v", err)
	}
	if d, err := svc.LoadDraft(id); err != nil || d.CreatedBy != "u-a" {
		t.Fatalf("draft owner = %q (%v), want u-a", d.CreatedBy, err)
	}
	pub, err := svc.Publish(id, "u-b")
	if err != nil {
		t.Fatalf("publish as b: %v", err)
	}
	if pub.CreatedBy != "u-a" {
		t.Fatalf("published owner = %q, want u-a", pub.CreatedBy)
	}
	if live, err := svc.Load(id); err != nil || live.CreatedBy != "u-a" {
		t.Fatalf("live owner = %q (%v), want u-a", live.CreatedBy, err)
	}
}

// TestFileService_PublishFillsAMissingOwner: the actor still fills an owner
// nobody set, so a workflow never ends up ownerless by accident.
func TestFileService_PublishFillsAMissingOwner(t *testing.T) {
	svc, _ := newFileSvc(t)
	id := "file-unowned"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SaveDraft(id, sampleWF(id)); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	pub, err := svc.Publish(id, "u-b")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if pub.CreatedBy != "u-b" {
		t.Fatalf("published owner = %q, want the publisher to fill the blank", pub.CreatedBy)
	}
}
