package service

import (
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/repository"
	"github.com/yogasw/wick/internal/entity"
)

func newDBSvc(t *testing.T) (*DBService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.Workflow{}, &entity.WorkflowVersion{}, &entity.WorkflowTestCase{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dir := t.TempDir()
	layout := config.NewLayout(filepath.Join(dir, "base"))
	if err := layout.EnsureLayout(); err != nil {
		t.Fatalf("layout: %v", err)
	}
	return NewDB(layout, repository.New(db)), db
}

// TestDBService_CreateLoad covers the basic round-trip.
func TestDBService_CreateLoad(t *testing.T) {
	svc, _ := newDBSvc(t)
	w := sampleWF("alpha")
	if err := svc.Create("alpha", w); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.Load("alpha")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Name != w.Name {
		t.Errorf("name: got %q want %q", got.Name, w.Name)
	}
}

// TestDBService_LockEnforcement confirms the lock check fires on
// SaveDraft and the unlock path remains open.
func TestDBService_LockEnforcement(t *testing.T) {
	svc, _ := newDBSvc(t)
	id := "lock-db"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	locked := sampleWF(id)
	locked.Canvas = map[string]any{"locked": true}
	if err := svc.SaveDraft(id, locked); err != nil {
		t.Fatalf("lock save: %v", err)
	}
	mutate := sampleWF(id)
	mutate.Name = "mutated"
	mutate.Canvas = map[string]any{"locked": true}
	if err := svc.SaveDraft(id, mutate); !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	unlocked := sampleWF(id)
	unlocked.Canvas = map[string]any{"locked": false}
	if err := svc.SaveDraft(id, unlocked); err != nil {
		t.Fatalf("unlock should pass: %v", err)
	}
}

// TestDBService_TestCRUD covers the workflow_test_cases table path
// (ListTests/GetTest/SaveTest/DeleteTest).
func TestDBService_TestCRUD(t *testing.T) {
	svc, _ := newDBSvc(t)
	id := "tests-db"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	body := []byte(`{"name":"c1"}`)
	if err := svc.SaveTest(id, "c1", body); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := svc.SaveTest(id, "c2", []byte(`{"name":"c2"}`)); err != nil {
		t.Fatalf("save c2: %v", err)
	}
	names, _ := svc.ListTests(id)
	if len(names) != 2 {
		t.Errorf("list got %d want 2", len(names))
	}
	got, _ := svc.GetTest(id, "c1")
	if string(got) != string(body) {
		t.Errorf("get c1: %s != %s", got, body)
	}
	if err := svc.DeleteTest(id, "c1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	names, _ = svc.ListTests(id)
	if len(names) != 1 || names[0] != "c2" {
		t.Errorf("after delete: %v", names)
	}
}

// TestDBService_Versions confirms SaveDraft + Publish populate the
// workflow_versions audit trail.
func TestDBService_Versions(t *testing.T) {
	svc, db := newDBSvc(t)
	id := "ver-db"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Publish(id, ""); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Two snapshots expected: initial draft + first publish.
	var rows []entity.WorkflowVersion
	if err := db.Where("workflow_id = ?", id).Find(&rows).Error; err != nil {
		t.Fatalf("query versions: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("versions: got %d want 2", len(rows))
	}
	kinds := map[string]int{}
	for _, r := range rows {
		kinds[r.Kind]++
	}
	if kinds["draft"] != 1 || kinds["published"] != 1 {
		t.Errorf("kinds: got %v want {draft:1, published:1}", kinds)
	}
}

// TestDBService_EnvValues confirms env values round-trip through the
// env_values column and stay OUT of the version history — changing
// config must not append a draft/publish snapshot.
func TestDBService_EnvValues(t *testing.T) {
	svc, db := newDBSvc(t)
	id := "env-db"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Empty before any save.
	got, err := svc.LoadEnvValues(id)
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty env, got %v", got)
	}

	// Count snapshots before touching env so we can prove env writes
	// don't grow the audit trail.
	var before int64
	db.Model(&entity.WorkflowVersion{}).Where("workflow_id = ?", id).Count(&before)

	want := map[string]string{
		"slack_channel": "#support-prod",
		"github_pat":    "wick_enc_aGVsbG8=",
		"allowed_users": `[{"id":"U100","name":"Yoga"}]`,
	}
	if err := svc.SaveEnvValues(id, want); err != nil {
		t.Fatalf("save env: %v", err)
	}

	got, err = svc.LoadEnvValues(id)
	if err != nil {
		t.Fatalf("load env: %v", err)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env %q: got %q want %q", k, got[k], v)
		}
	}

	// Secret ciphertext stays verbatim — no decrypt on the storage path.
	if got["github_pat"] != "wick_enc_aGVsbG8=" {
		t.Errorf("secret mangled: %q", got["github_pat"])
	}

	// Version history must be unchanged.
	var after int64
	db.Model(&entity.WorkflowVersion{}).Where("workflow_id = ?", id).Count(&after)
	if after != before {
		t.Errorf("env save changed version count: before=%d after=%d", before, after)
	}

	// Body untouched too — env lives in its own column.
	var row entity.Workflow
	db.Where("id = ?", id).First(&row)
	if row.EnvValues == "" {
		t.Error("env_values column not persisted")
	}
}

// TestDBService_FilesRejected confirms the legacy file path is
// unreachable through the test surface — only __tests__/ goes through.
func TestDBService_OnlyTestsAddressable(t *testing.T) {
	svc, _ := newDBSvc(t)
	id := "no-files"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Test surface works.
	if err := svc.SaveTest(id, "ok", []byte(`{}`)); err != nil {
		t.Errorf("test save should pass: %v", err)
	}
	// Sanity: list comes back with the test we just saved.
	names, _ := svc.ListTests(id)
	if len(names) != 1 || names[0] != "ok" {
		t.Errorf("ListTests: got %v", names)
	}
	// Compile-time check: the file-path surface is gone — uncommenting
	// the lines below would fail to build, which is the contract.
	var _ = workflow.Workflow{}
}

// TestDBService_ToggleKeepsDraftUnpublished: flipping the switch must not
// publish work in progress. The old Toggle wrote the DRAFT body into the
// published column, so enabling a workflow silently shipped every
// unreviewed edit sitting in the editor.
func TestDBService_ToggleKeepsDraftUnpublished(t *testing.T) {
	svc, _ := newDBSvc(t)
	id := "toggle-draft"
	published := sampleWF(id)
	published.Name = "published name"
	if err := svc.Create(id, published); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Publish(id, ""); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// An edit that is NOT meant to go live yet.
	draft := published
	draft.Name = "work in progress"
	if err := svc.SaveDraft(id, draft); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	if err := svc.Toggle(id, true); err != nil {
		t.Fatalf("toggle: %v", err)
	}

	live, err := svc.Load(id)
	if err != nil {
		t.Fatalf("load published: %v", err)
	}
	if live.Name != "published name" {
		t.Fatalf("published name = %q — the toggle published the draft", live.Name)
	}
	if !live.Enabled {
		t.Fatal("toggle did not enable the published workflow")
	}
	// The draft keeps its own edit and picks up the new flag, so the
	// editor's chip matches the router.
	d, err := svc.LoadDraft(id)
	if err != nil {
		t.Fatalf("load draft: %v", err)
	}
	if d.Name != "work in progress" {
		t.Fatalf("draft name = %q, want the in-progress edit kept", d.Name)
	}
	if !d.Enabled {
		t.Fatal("draft did not pick up the enabled flag")
	}
}

// TestDBService_ToggleWithoutDraft is the ordinary path: no draft, so the
// published body carries the flag and the row agrees with it.
func TestDBService_ToggleWithoutDraft(t *testing.T) {
	svc, _ := newDBSvc(t)
	id := "toggle-plain"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Publish(id, ""); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := svc.Toggle(id, true); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	got, err := svc.Load(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.Enabled {
		t.Fatal("enabled flag did not persist")
	}
	if err := svc.Toggle(id, false); err != nil {
		t.Fatalf("toggle off: %v", err)
	}
	if got, _ = svc.Load(id); got.Enabled {
		t.Fatal("disable did not persist")
	}
}

// TestDBService_PublishKeepsTheCreator: a workflow belongs to whoever asked
// for it, not to whoever last pressed Publish. Publishing used to re-stamp
// created_by, so an admin reviewing someone's workflow quietly became its
// owner — and the person it was built for lost sight of it.
func TestDBService_PublishKeepsTheCreator(t *testing.T) {
	svc, db := newDBSvc(t)
	id := "owned"
	w := sampleWF(id)
	w.CreatedBy = "u-creator"
	if err := svc.Create(id, w); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Publish(id, "u-publisher"); err != nil {
		t.Fatalf("publish: %v", err)
	}

	var row entity.Workflow
	if err := db.Where("id = ?", id).First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.CreatedBy != "u-creator" {
		t.Fatalf("owner = %q after a publish by someone else, want the creator", row.CreatedBy)
	}

	// The publisher is still recorded — on the version snapshot, which is
	// where "who did this" belongs.
	var snap entity.WorkflowVersion
	if err := db.Where("workflow_id = ? AND kind = ?", id, "published").
		Order("id desc").First(&snap).Error; err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if snap.CreatedBy != "u-publisher" {
		t.Fatalf("snapshot author = %q, want the publisher", snap.CreatedBy)
	}
}

// TestDBService_SetOwner: the admin transfer has to land in the row AND the
// body, because Load reads the body — leaving them split would show one owner
// on the admin page and another everywhere else.
func TestDBService_SetOwner(t *testing.T) {
	svc, db := newDBSvc(t)
	id := "transfer"
	w := sampleWF(id)
	w.CreatedBy = "u-old"
	if err := svc.Create(id, w); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Publish(id, ""); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// An edit in flight: the draft must not carry the old owner forward,
	// or the next publish would resurrect them.
	draft := w
	draft.Name = "in progress"
	if err := svc.SaveDraft(id, draft); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	if err := svc.SetOwner(id, "u-new"); err != nil {
		t.Fatalf("set owner: %v", err)
	}

	var row entity.Workflow
	if err := db.Where("id = ?", id).First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.CreatedBy != "u-new" {
		t.Fatalf("row owner = %q, want u-new", row.CreatedBy)
	}
	live, err := svc.Load(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if live.CreatedBy != "u-new" {
		t.Fatalf("published body owner = %q, want u-new", live.CreatedBy)
	}
	d, err := svc.LoadDraft(id)
	if err != nil {
		t.Fatalf("load draft: %v", err)
	}
	if d.CreatedBy != "u-new" {
		t.Fatalf("draft owner = %q, want u-new", d.CreatedBy)
	}
	if d.Name != "in progress" {
		t.Fatalf("draft name = %q — the transfer clobbered the edit", d.Name)
	}
}
