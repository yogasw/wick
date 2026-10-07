package replace

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/yogasw/wick/internal/entity"
	pkgentity "github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
)

const (
	oldKey = "notion-ticket-sync"
	newKey = "notion_ticket_sync"
	// A stored ciphertext; the migration must copy it byte for byte.
	cipher = "wick_enc_v1:ZmFrZS1jaXBoZXJ0ZXh0LW5ldmVyLWRlY3J5cHRlZA"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&pkgentity.Config{}, &entity.Job{}, &entity.JobRun{}, &entity.ToolTag{},
		&entity.ToolPermission{}, &entity.Bookmark{}, &entity.PluginReplacement{}, &entity.PluginAudit{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func decls() []pkgentity.Config {
	return []pkgentity.Config{
		{Key: "token", IsSecret: true},
		{Key: "base_url", Value: "https://api.notion.com"},
		{Key: "dry_run", Value: "false"},
		{Key: "label"},
	}
}

func jobPair() Pair {
	return Pair{Kind: KindJob, Old: oldKey, New: newKey, Configs: decls(), DefaultCron: "*/5 * * * *"}
}

// seedOld writes the built-in's state: secret, non-secret values, an enabled
// every-minute job with run history, tags, visibility and a bookmark.
func seedOld(t *testing.T, db *gorm.DB) entity.Job {
	t.Helper()
	must(t, db.Create(&pkgentity.Config{Owner: oldKey, Key: "token", Value: cipher, IsSecret: true}).Error)
	must(t, db.Create(&pkgentity.Config{Owner: oldKey, Key: "base_url", Value: "https://proxy.example"}).Error)
	must(t, db.Create(&pkgentity.Config{Owner: oldKey, Key: "dry_run", Value: "true"}).Error)
	must(t, db.Create(&pkgentity.Config{Owner: oldKey, Key: "legacy_only", Value: "x"}).Error)
	old := entity.Job{Key: oldKey, Name: "Notion Ticket Sync", Schedule: "* * * * *", Enabled: true, MaxRuns: 7, MaxTimeoutMin: 5}
	must(t, db.Create(&old).Error)
	must(t, db.Create(&entity.JobRun{ID: "r1", JobID: old.ID, Status: "success"}).Error)
	must(t, db.Create(&entity.JobRun{ID: "r2", JobID: old.ID, Status: "success"}).Error)
	must(t, db.Create(&entity.ToolTag{ToolPath: "/jobs/" + oldKey, TagID: "tag-support"}).Error)
	must(t, db.Create(&entity.ToolPermission{ToolPath: "/jobs/" + oldKey, Visibility: entity.VisibilityPublic}).Error)
	must(t, db.Create(&entity.Bookmark{UserID: "u1", ToolPath: "/jobs/" + oldKey}).Error)
	return old
}

func cfg(t *testing.T, db *gorm.DB, owner, key string) string {
	t.Helper()
	c, err := findConfig(db, owner, key)
	must(t, err)
	if c == nil {
		return "<missing>"
	}
	return c.Value
}

func jobRow(t *testing.T, db *gorm.DB, key string) *entity.Job {
	t.Helper()
	j, err := findJob(db, key)
	must(t, err)
	if j == nil {
		t.Fatalf("job %s missing", key)
	}
	return j
}

func TestApplyMovesConfigSecretJobAndAccess(t *testing.T) {
	db := newDB(t)
	seedOld(t, db)
	m := New(db)
	ctx := context.Background()

	rep, err := m.Apply(ctx, jobPair(), "system", false)
	must(t, err)
	if rep.AlreadyDone || rep.MigratedAt == nil {
		t.Fatalf("first apply should migrate: %+v", rep)
	}
	if got := cfg(t, db, newKey, "token"); got != cipher {
		t.Fatalf("secret not copied as stored ciphertext: %q", got)
	}
	if got := cfg(t, db, newKey, "base_url"); got != "https://proxy.example" {
		t.Fatalf("base_url = %q", got)
	}
	if got := cfg(t, db, newKey, "legacy_only"); got != "<missing>" {
		t.Fatalf("undeclared field must not move, got %q", got)
	}
	c, _ := findConfig(db, newKey, "token")
	if !c.IsSecret {
		t.Fatal("copied secret row lost its secret flag")
	}

	nj := jobRow(t, db, newKey)
	if nj.Schedule != "* * * * *" || !nj.Enabled || nj.MaxRuns != 7 || nj.MaxTimeoutMin != 5 {
		t.Fatalf("job settings not moved: %+v", nj)
	}
	if oj := jobRow(t, db, oldKey); oj.Enabled {
		t.Fatal("old job still enabled — both would run")
	}
	if rep.Job == nil || rep.Job.HistoryRuns != 2 || !rep.Job.OldDisabled {
		t.Fatalf("job report: %+v", rep.Job)
	}

	var n int64
	db.Model(&entity.ToolTag{}).Where("tool_path = ? AND tag_id = ?", "/jobs/"+newKey, "tag-support").Count(&n)
	if n != 1 {
		t.Fatal("tag not merged onto the new path")
	}
	var perm entity.ToolPermission
	must(t, db.Where("tool_path = ?", "/jobs/"+newKey).Take(&perm).Error)
	if perm.Visibility != entity.VisibilityPublic {
		t.Fatalf("visibility = %q", perm.Visibility)
	}
	db.Model(&entity.Bookmark{}).Where("user_id = ? AND tool_path = ?", "u1", "/jobs/"+newKey).Count(&n)
	if n != 1 {
		t.Fatal("bookmark not copied")
	}
	db.Model(&entity.PluginAudit{}).Where("action = ? AND key = ?", "replace.migrate", newKey).Count(&n)
	if n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
	// The summary goes to logs/audit: it must never carry a value.
	if s := rep.Summary(); contains(s, cipher) || contains(s, "proxy.example") {
		t.Fatalf("summary leaks a value: %s", s)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestApplyIsIdempotentAndKeepsOldJobDisabled(t *testing.T) {
	db := newDB(t)
	seedOld(t, db)
	m := New(db)
	ctx := context.Background()
	_, err := m.Apply(ctx, jobPair(), "system", false)
	must(t, err)

	// Admin edits the plugin after the move, and someone re-enables the old row.
	must(t, db.Model(&pkgentity.Config{}).Where("owner = ? AND key = ?", newKey, "base_url").Update("value", "https://edited").Error)
	must(t, db.Model(&entity.Job{}).Where("key = ?", newKey).Update("schedule", "0 * * * *").Error)
	must(t, db.Model(&entity.Job{}).Where("key = ?", oldKey).Update("enabled", true).Error)

	rep, err := m.Apply(ctx, jobPair(), "system", false)
	must(t, err)
	if !rep.AlreadyDone {
		t.Fatal("second apply should be a no-op")
	}
	if got := cfg(t, db, newKey, "base_url"); got != "https://edited" {
		t.Fatalf("second apply overwrote a manual value: %q", got)
	}
	if got := jobRow(t, db, newKey).Schedule; got != "0 * * * *" {
		t.Fatalf("second apply overwrote the schedule: %q", got)
	}
	if jobRow(t, db, oldKey).Enabled {
		t.Fatal("old job must be disabled again on every apply")
	}
	var n int64
	db.Model(&entity.PluginAudit{}).Count(&n)
	if n != 1 {
		t.Fatalf("idempotent apply wrote audit again: %d rows", n)
	}
}

func TestApplyNeverOverwritesManualPluginValues(t *testing.T) {
	db := newDB(t)
	seedOld(t, db)
	// The plugin was configured by hand before the migration ran.
	must(t, db.Create(&pkgentity.Config{Owner: newKey, Key: "token", Value: "wick_enc_v1:manual", IsSecret: true}).Error)
	must(t, db.Create(&pkgentity.Config{Owner: newKey, Key: "base_url", Value: "https://manual"}).Error)
	// Still at its declared default "false" → filled from the old key.
	must(t, db.Create(&pkgentity.Config{Owner: newKey, Key: "dry_run", Value: "false"}).Error)
	nj := entity.Job{Key: newKey, Name: "x", Schedule: "0 9 * * *", Enabled: true}
	must(t, db.Create(&nj).Error)

	rep, err := New(db).Apply(context.Background(), jobPair(), "system", false)
	must(t, err)
	if got := cfg(t, db, newKey, "token"); got != "wick_enc_v1:manual" {
		t.Fatalf("manual secret overwritten: %q", got)
	}
	if got := cfg(t, db, newKey, "base_url"); got != "https://manual" {
		t.Fatalf("manual value overwritten: %q", got)
	}
	if got := cfg(t, db, newKey, "dry_run"); got != "true" {
		t.Fatalf("default value not filled: %q", got)
	}
	if j := jobRow(t, db, newKey); j.Schedule != "0 9 * * *" {
		t.Fatalf("configured job overwritten: %+v", j)
	}
	if rep.Job.Action != ActionKeep {
		t.Fatalf("job action = %s", rep.Job.Action)
	}
	if jobRow(t, db, oldKey).Enabled {
		t.Fatal("old job must be disabled even when settings are kept")
	}
}

func TestPlanWritesNothingAndMasksSecrets(t *testing.T) {
	db := newDB(t)
	seedOld(t, db)
	rep, err := New(db).Plan(context.Background(), jobPair())
	must(t, err)
	if !rep.DryRun {
		t.Fatal("plan must be marked dry_run")
	}
	for _, f := range rep.Configs {
		if f.Key == "token" && (f.Old != "set" || f.New != "empty" || f.Action != ActionCopy) {
			t.Fatalf("secret field report: %+v", f)
		}
		if f.Secret && (f.Old == cipher || f.New == cipher) {
			t.Fatal("plan leaks a secret")
		}
	}
	if got := cfg(t, db, newKey, "token"); got != "<missing>" {
		t.Fatal("plan wrote a config row")
	}
	if !jobRow(t, db, oldKey).Enabled {
		t.Fatal("plan disabled the old job")
	}
	var n int64
	db.Model(&entity.PluginReplacement{}).Count(&n)
	if n != 0 {
		t.Fatal("plan wrote the marker")
	}
}

func TestSecretFlagMismatchIsSkipped(t *testing.T) {
	db := newDB(t)
	must(t, db.Create(&pkgentity.Config{Owner: oldKey, Key: "label", Value: cipher, IsSecret: true}).Error)
	_, err := New(db).Apply(context.Background(), jobPair(), "system", false)
	must(t, err)
	if got := cfg(t, db, newKey, "label"); got != "<missing>" {
		t.Fatalf("ciphertext copied into a plain field: %q", got)
	}
}

func TestPrepareUnregistersOldAndHidesJob(t *testing.T) {
	reset()
	t.Cleanup(reset)
	jobMods := []job.Module{
		{Meta: job.Meta{Key: oldKey}},
		{Meta: job.Meta{Key: newKey, Replaces: []string{oldKey, newKey, ""}}},
	}
	toolMods := []tool.Module{
		{Meta: tool.Tool{Key: "beautify-json"}},
		{Meta: tool.Tool{Key: "beautify_json", Replaces: []string{"beautify-json"}}},
	}
	var goneJobs, goneTools []string
	pairs := Prepare(jobMods, toolMods,
		func(k string) bool { goneJobs = append(goneJobs, k); return true },
		func(k string) bool { goneTools = append(goneTools, k); return true })
	if len(pairs) != 2 {
		t.Fatalf("pairs = %+v", pairs)
	}
	if len(goneJobs) != 1 || goneJobs[0] != oldKey || len(goneTools) != 1 || goneTools[0] != "beautify-json" {
		t.Fatalf("unregistered jobs=%v tools=%v", goneJobs, goneTools)
	}
	if !IsReplaced(oldKey) || IsReplaced(newKey) {
		t.Fatal("IsReplaced wrong")
	}
	if got := Lookup("beautify_json"); len(got) != 1 || got[0].Old != "beautify-json" || got[0].Kind != KindTool {
		t.Fatalf("Lookup = %+v", got)
	}
}

func TestToolAccessMovesOnToolPath(t *testing.T) {
	db := newDB(t)
	must(t, db.Create(&pkgentity.Config{Owner: "beautify-json", Key: "indent", Value: "4"}).Error)
	must(t, db.Create(&entity.ToolTag{ToolPath: "/tools/beautify-json", TagID: "t1"}).Error)
	must(t, db.Create(&entity.ToolPermission{ToolPath: "/tools/beautify-json", Visibility: entity.VisibilityPublic, Disabled: true}).Error)
	p := Pair{Kind: KindTool, Old: "beautify-json", New: "beautify_json", Configs: []pkgentity.Config{{Key: "indent", Value: "2"}}}
	rep, err := New(db).Apply(context.Background(), p, "system", false)
	must(t, err)
	if rep.Job != nil {
		t.Fatal("a tool pair has no job report")
	}
	if got := cfg(t, db, "beautify_json", "indent"); got != "4" {
		t.Fatalf("indent = %q", got)
	}
	var perm entity.ToolPermission
	must(t, db.Where("tool_path = ?", "/tools/beautify_json").Take(&perm).Error)
	if perm.Visibility != entity.VisibilityPublic || !perm.Disabled {
		t.Fatalf("permission = %+v", perm)
	}
}
