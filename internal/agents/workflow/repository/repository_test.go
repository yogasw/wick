package repository

import (
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/entity"
)

func openMem(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.Workflow{}, &entity.WorkflowVersion{}, &entity.WorkflowTestCase{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

func sampleWorkflow(id string) wf.Workflow {
	return wf.Workflow{
		ID:      id,
		Name:    "sample-" + id,
		Enabled: false,
		Graph: wf.Graph{
			Entry: "n1",
			Nodes: []wf.Node{{ID: "n1", Type: wf.NodeEnd}},
		},
	}
}

// TestCreateAndGet ensures the row round-trips.
func TestCreateAndGet(t *testing.T) {
	r := New(openMem(t))
	if err := r.Create("alpha", "Alpha", "yoga"); err != nil {
		t.Fatalf("create: %v", err)
	}
	row, err := r.Get("alpha")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Name != "Alpha" {
		t.Errorf("name: got %q want Alpha", row.Name)
	}
	if row.CreatedBy != "yoga" {
		t.Errorf("created_by: got %q want yoga", row.CreatedBy)
	}
	if row.HasDraft {
		t.Error("has_draft should default false on fresh row")
	}
}

// TestSaveDraftThenPublish covers the canonical edit-then-publish flow.
func TestSaveDraftThenPublish(t *testing.T) {
	r := New(openMem(t))
	if err := r.Create("beta", "Beta", "yoga"); err != nil {
		t.Fatalf("create: %v", err)
	}

	w := sampleWorkflow("beta")
	w.Name = "Beta draft"
	if _, err := r.SaveDraft("beta", w, "yoga", "first edit"); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	row, err := r.Get("beta")
	if err != nil {
		t.Fatalf("get after save: %v", err)
	}
	if !row.HasDraft {
		t.Error("has_draft should flip true after save")
	}
	if !strings.Contains(row.BodyDraft, `"name": "Beta draft"`) {
		t.Errorf("draft body missing renamed value: %s", row.BodyDraft)
	}
	if row.BodyPublished != "" {
		t.Error("published yaml should still be empty before publish")
	}

	if _, err := r.Publish("beta", "yoga", "ship it"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	row, _ = r.Get("beta")
	if row.HasDraft {
		t.Error("has_draft should be false after publish")
	}
	if !strings.Contains(row.BodyPublished, `"name": "Beta draft"`) {
		t.Errorf("published body missing renamed value: %s", row.BodyPublished)
	}
	if row.BodyDraft != "" {
		t.Errorf("draft yaml should be cleared after publish: %s", row.BodyDraft)
	}

	vs, err := r.Versions("beta")
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("versions: got %d want 2 (1 draft + 1 published)", len(vs))
	}
	if vs[0].Kind != "published" || vs[1].Kind != "draft" {
		t.Errorf("versions ordering: got %s,%s want published,draft", vs[0].Kind, vs[1].Kind)
	}
}

// TestDiscardDraft clears the draft slot without touching published or
// the history.
func TestDiscardDraft(t *testing.T) {
	r := New(openMem(t))
	_ = r.Create("g", "G", "")
	_, _ = r.SaveDraft("g", sampleWorkflow("g"), "", "")
	if err := r.DiscardDraft("g"); err != nil {
		t.Fatalf("discard: %v", err)
	}
	row, _ := r.Get("g")
	if row.HasDraft || row.BodyDraft != "" {
		t.Errorf("draft not cleared: has=%v yaml=%q", row.HasDraft, row.BodyDraft)
	}
}

// TestRestoreCopiesIntoDraft confirms restore writes the snapshot back
// to the draft slot — never auto-publishes.
func TestRestoreCopiesIntoDraft(t *testing.T) {
	r := New(openMem(t))
	_ = r.Create("h", "H", "")

	w1 := sampleWorkflow("h")
	w1.Name = "rev-1"
	v1, _ := r.SaveDraft("h", w1, "", "")
	_, _ = r.Publish("h", "", "")

	w2 := sampleWorkflow("h")
	w2.Name = "rev-2"
	_, _ = r.SaveDraft("h", w2, "", "")
	_, _ = r.Publish("h", "", "")

	// Restore from v1 (the original draft snapshot).
	if _, err := r.Restore("h", v1, "yoga"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	row, _ := r.Get("h")
	if !row.HasDraft {
		t.Error("restore should leave HasDraft=true")
	}
	if !strings.Contains(row.BodyDraft, `"name": "rev-1"`) {
		t.Errorf("restored draft missing rev-1: %s", row.BodyDraft)
	}
	if !strings.Contains(row.BodyPublished, `"name": "rev-2"`) {
		t.Errorf("published should still be rev-2; got: %s", row.BodyPublished)
	}
}

// TestDraftRetentionPrunes confirms draft history doesn't grow forever.
// Set DraftRetention low for the test via a fresh repo + many saves.
func TestDraftRetentionPrunes(t *testing.T) {
	r := New(openMem(t))
	_ = r.Create("i", "I", "")
	const total = DraftRetention + 5
	for i := 0; i < total; i++ {
		w := sampleWorkflow("i")
		w.Name = "rev"
		if _, err := r.SaveDraft("i", w, "", ""); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	vs, _ := r.Versions("i")
	if got := len(vs); got > DraftRetention {
		t.Errorf("draft versions not pruned: got %d want <= %d", got, DraftRetention)
	}
}

// TestDelete wipes the workflow + every version + every test case.
func TestDelete(t *testing.T) {
	r := New(openMem(t))
	_ = r.Create("z", "Z", "")
	_, _ = r.SaveDraft("z", sampleWorkflow("z"), "", "")
	_ = r.db.Create(&entity.WorkflowTestCase{WorkflowID: "z", Name: "t1", Body: "{}"}).Error

	if err := r.Delete("z"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.Get("z"); err == nil {
		t.Error("get should fail after delete")
	}
	vs, _ := r.Versions("z")
	if len(vs) != 0 {
		t.Errorf("versions remain after delete: %d", len(vs))
	}
}

// TestList ordering — newest updated_at first.
func TestList(t *testing.T) {
	r := New(openMem(t))
	_ = r.Create("a", "A", "")
	_ = r.Create("b", "B", "")
	_, _ = r.SaveDraft("a", sampleWorkflow("a"), "", "")
	rows, err := r.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len rows: %d", len(rows))
	}
	if rows[0].ID != "a" {
		t.Errorf("expected a first (just edited), got %s", rows[0].ID)
	}
}
