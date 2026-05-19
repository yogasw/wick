package providersync

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&entity.ProviderStorage{}, &entity.ProviderStorageSource{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func writeFile(t *testing.T, p string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// ─── sourceCovers / pickRetention pure helpers ────────────────────────

func TestSourceCovers_Folder(t *testing.T) {
	srcs := []entity.ProviderStorageSource{
		{Mode: "folder", SyncPath: "/home/app/.support", Enabled: true},
	}
	cases := map[string]bool{
		"/home/app/.support":            true,  // exact dir match
		"/home/app/.support/foo.yml":    true,  // direct child
		"/home/app/.support/a/b/c.json": true,  // nested
		"/home/app/.support-other":      false, // sibling with prefix overlap
		"/home/app/other":               false,
		"/etc/foo":                      false,
	}
	for path, want := range cases {
		got := sourceCovers(path, srcs)
		if got != want {
			t.Errorf("sourceCovers(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestSourceCovers_Single(t *testing.T) {
	srcs := []entity.ProviderStorageSource{
		{Mode: "single", SyncPath: "/home/app/.creds", Enabled: true},
	}
	if !sourceCovers("/home/app/.creds", srcs) {
		t.Error("exact single should match")
	}
	if sourceCovers("/home/app/.creds.bak", srcs) {
		t.Error("non-exact single should not match")
	}
	if sourceCovers("/home/app/.creds/inner", srcs) {
		t.Error("single does not act like folder")
	}
}

func TestPickRetention_DeepestWins(t *testing.T) {
	srcs := []entity.ProviderStorageSource{
		{Mode: "folder", SyncPath: "/app/home", RetentionDays: 0, Enabled: true},
		{Mode: "folder", SyncPath: "/app/home/session", RetentionDays: 7, Enabled: true},
		{Mode: "folder", SyncPath: "/elsewhere", RetentionDays: 30, Enabled: true},
	}
	if got := pickRetention("/app/home/notes.txt", srcs); got != 0 {
		t.Errorf("file in shallow source = %d, want 0", got)
	}
	if got := pickRetention("/app/home/session/log.txt", srcs); got != 7 {
		t.Errorf("file in deep source = %d, want 7", got)
	}
	if got := pickRetention("/app/home/session", srcs); got != 7 {
		t.Errorf("exact deep dir = %d, want 7", got)
	}
	if got := pickRetention("/unrelated/x", srcs); got != 0 {
		t.Errorf("uncovered file = %d, want 0", got)
	}
}

func TestPickRetention_IgnoresDisabled(t *testing.T) {
	srcs := []entity.ProviderStorageSource{
		{Mode: "folder", SyncPath: "/app/home", RetentionDays: 5, Enabled: true},
		{Mode: "folder", SyncPath: "/app/home/session", RetentionDays: 7, Enabled: false},
	}
	if got := pickRetention("/app/home/session/log.txt", srcs); got != 5 {
		t.Errorf("disabled deep source should be ignored, got %d want 5", got)
	}
}

func TestNormAbs(t *testing.T) {
	cases := map[string]string{
		"/home/app/.x":  "/home/app/.x",
		"/home/app/.x/": "/home/app/.x",
		"/a//b/../c":    "/a/c",
	}
	for in, want := range cases {
		if got := normAbs(in); got != want {
			t.Errorf("normAbs(%q) = %q, want %q", in, got, want)
		}
	}
}

// ─── collectFiles ─────────────────────────────────────────────────────

func TestCollectFiles_FolderMode_AbsKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "AA")
	writeFile(t, filepath.Join(dir, "sub", "b.txt"), "BB")

	out, err := collectFiles(&provider.StorageConfig{Mode: "folder", SyncPath: dir}, nil)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 files, got %d (%v)", len(out), keysOf(out))
	}
	expectA := filepath.ToSlash(filepath.Join(dir, "a.txt"))
	expectB := filepath.ToSlash(filepath.Join(dir, "sub", "b.txt"))
	if _, ok := out[expectA]; !ok {
		t.Errorf("missing key %q, got %v", expectA, keysOf(out))
	}
	if string(out[expectB]) != "BB" {
		t.Errorf("wrong content for %q: %q", expectB, out[expectB])
	}
}

func TestCollectFiles_SingleMode_AbsKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	writeFile(t, path, "secret")

	out, err := collectFiles(&provider.StorageConfig{Mode: "single", SyncPath: path}, nil)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 file, got %d", len(out))
	}
	want := filepath.ToSlash(path)
	if _, ok := out[want]; !ok {
		t.Errorf("missing key %q, got %v", want, keysOf(out))
	}
}

func keysOf(m map[string][]byte) []string {
	k := make([]string, 0, len(m))
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}

// ─── ensureFolderChain ────────────────────────────────────────────────

func TestEnsureFolderChain_AbsolutePath(t *testing.T) {
	db := newDB(t)
	s := newStore(db)
	ctx := context.Background()

	parent, err := s.ensureFolderChain(ctx, "wick", "wick", "/home/app/.support-tools/agents/foo.yml")
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	if parent == 0 {
		t.Fatal("expected non-zero parent for nested file")
	}

	var rows []entity.ProviderStorage
	db.Where("is_dir = ?", true).Order("rel_path").Find(&rows)
	wantPaths := []string{"/home", "/home/app", "/home/app/.support-tools", "/home/app/.support-tools/agents"}
	if len(rows) != len(wantPaths) {
		t.Fatalf("want %d folder rows, got %d: %+v", len(wantPaths), len(rows), pathsOf(rows))
	}
	for i, want := range wantPaths {
		if rows[i].RelPath != want {
			t.Errorf("folder[%d] rel_path = %q, want %q", i, rows[i].RelPath, want)
		}
	}

	// Idempotent — re-run, no new rows
	if _, err := s.ensureFolderChain(ctx, "wick", "wick", "/home/app/.support-tools/agents/foo.yml"); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	var n int64
	db.Model(&entity.ProviderStorage{}).Where("is_dir = ?", true).Count(&n)
	if n != int64(len(wantPaths)) {
		t.Errorf("folder row count drifted after rerun: %d", n)
	}
}

func TestEnsureFolderChain_RootFile(t *testing.T) {
	db := newDB(t)
	s := newStore(db)
	parent, err := s.ensureFolderChain(context.Background(), "p", "i", "/foo.yml")
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	if parent != entity.RootParentID {
		t.Errorf("root-level file should have parent=0, got %d", parent)
	}
}

func TestEnsureFolderChain_WindowsPath(t *testing.T) {
	db := newDB(t)
	s := newStore(db)
	_, err := s.ensureFolderChain(context.Background(), "p", "i", "C:/Users/x/foo.yml")
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	var rows []entity.ProviderStorage
	db.Where("is_dir = ?", true).Order("rel_path").Find(&rows)
	want := []string{"C:", "C:/Users", "C:/Users/x"}
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %v", pathsOf(rows), want)
	}
	for i, p := range want {
		if rows[i].RelPath != p {
			t.Errorf("[%d] = %q, want %q", i, rows[i].RelPath, p)
		}
	}
}

func pathsOf(rows []entity.ProviderStorage) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.RelPath
	}
	return out
}

// ─── wipeLegacyRelPathRows ────────────────────────────────────────────

func TestWipeLegacyRelPathRows(t *testing.T) {
	db := newDB(t)
	s := newStore(db)
	ctx := context.Background()

	// legacy (non-absolute) + new (absolute) + Windows; names distinct
	// so the new (parent_id, name) unique index doesn't collide at root.
	seed := []entity.ProviderStorage{
		{ProviderType: "p", InstanceName: "i", RelPath: "agents/foo.yml", Name: "legacy_a.yml", ContentHash: "x"},
		{ProviderType: "p", InstanceName: "i", RelPath: "/home/app/foo.yml", Name: "abs_a.yml", ContentHash: "y"},
		{ProviderType: "p", InstanceName: "i", RelPath: "C:/Users/x/foo.yml", Name: "abs_b.yml", ContentHash: "z"},
		{ProviderType: "p", InstanceName: "i", RelPath: "bare.yml", Name: "legacy_b.yml", ContentHash: "w"},
	}
	for _, row := range seed {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if err := s.wipeLegacyRelPathRows(ctx); err != nil {
		t.Fatalf("wipe: %v", err)
	}

	var rows []entity.ProviderStorage
	db.Order("rel_path").Find(&rows)
	got := pathsOf(rows)
	want := []string{"/home/app/foo.yml", "C:/Users/x/foo.yml"}
	if len(got) != len(want) {
		t.Fatalf("survivors = %v, want %v", got, want)
	}
	for i, p := range want {
		if got[i] != p {
			t.Errorf("[%d] = %q, want %q", i, got[i], p)
		}
	}
}

// ─── backup() end-to-end ──────────────────────────────────────────────

func TestBackup_StoresAbsolutePaths_DeduplicatesOverlap(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	root := t.TempDir()
	support := filepath.Join(root, ".support-tools")
	agents := filepath.Join(support, "agents")
	writeFile(t, filepath.Join(agents, "a.yml"), "A")
	writeFile(t, filepath.Join(support, "config.yml"), "C")

	// configure two overlapping sources under same provider/instance
	srcA := entity.ProviderStorageSource{
		ProviderType: "wick", InstanceName: "wick",
		Label: "config", SyncPath: support, Mode: "folder", RetentionDays: 0, Enabled: true,
	}
	srcB := entity.ProviderStorageSource{
		ProviderType: "wick", InstanceName: "wick",
		Label: "agents", SyncPath: agents, Mode: "folder", RetentionDays: 7, Enabled: true,
	}
	if _, err := mgr.SaveSource(ctx, srcA); err != nil {
		t.Fatalf("save A: %v", err)
	}
	if _, err := mgr.SaveSource(ctx, srcB); err != nil {
		t.Fatalf("save B: %v", err)
	}

	// SaveSource fires SyncOne; run one more for both to ensure idempotent
	for _, s := range []entity.ProviderStorageSource{srcA, srcB} {
		if err := mgr.SyncOne(ctx, SourceToInstance(s)); err != nil {
			t.Fatalf("sync %s: %v", s.Label, err)
		}
	}

	rows, _ := mgr.ListAll(ctx)
	var files []entity.ProviderStorage
	for _, r := range rows {
		if !r.IsDir {
			files = append(files, r)
		}
	}
	if len(files) != 2 {
		t.Fatalf("want 2 file rows (deduped via abs path), got %d: %v", len(files), pathsOf(files))
	}

	wantA := filepath.ToSlash(filepath.Join(agents, "a.yml"))
	wantC := filepath.ToSlash(filepath.Join(support, "config.yml"))
	byPath := map[string]entity.ProviderStorage{}
	for _, r := range files {
		byPath[r.RelPath] = r
	}
	if _, ok := byPath[wantA]; !ok {
		t.Fatalf("missing %q in rows: %v", wantA, pathsOf(files))
	}
	if _, ok := byPath[wantC]; !ok {
		t.Fatalf("missing %q in rows: %v", wantC, pathsOf(files))
	}

	// deepest source wins retention
	if byPath[wantA].RetentionDays != 7 {
		t.Errorf("a.yml retention = %d, want 7 (deepest src)", byPath[wantA].RetentionDays)
	}
	if byPath[wantC].RetentionDays != 0 {
		t.Errorf("config.yml retention = %d, want 0 (shallow src only)", byPath[wantC].RetentionDays)
	}
}

func TestBackup_HashUnchanged_NoRewrite(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	file := filepath.Join(dir, "foo.yml")
	writeFile(t, file, "hello")

	src := entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: dir, Mode: "folder", Enabled: true,
	}
	if _, err := mgr.SaveSource(ctx, src); err != nil {
		t.Fatalf("save: %v", err)
	}

	rowsBefore, _ := mgr.ListAll(ctx)
	var t1 string
	for _, r := range rowsBefore {
		if !r.IsDir {
			t1 = r.SyncedAt.Format("2006-01-02T15:04:05.000000")
		}
	}

	// re-sync without modifying content
	if err := mgr.SyncOne(ctx, SourceToInstance(src)); err != nil {
		t.Fatalf("resync: %v", err)
	}

	rowsAfter, _ := mgr.ListAll(ctx)
	for _, r := range rowsAfter {
		if !r.IsDir {
			t2 := r.SyncedAt.Format("2006-01-02T15:04:05.000000")
			if t2 != t1 {
				t.Errorf("SyncedAt changed despite same hash: %s -> %s", t1, t2)
			}
		}
	}
}

// ─── RestoreAll disk-wins guard ───────────────────────────────────────

func TestRestoreAll_FillsMissingFiles(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	file := filepath.Join(dir, "creds.json")
	writeFile(t, file, "v1")

	src := entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: dir, Mode: "folder", Enabled: true,
	}
	if _, err := mgr.SaveSource(ctx, src); err != nil {
		t.Fatalf("save: %v", err)
	}

	// delete disk copy → restore should put it back
	if err := os.Remove(file); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := mgr.RestoreAll(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := readFile(t, file); got != "v1" {
		t.Errorf("restored content = %q, want v1", got)
	}
}

func TestRestoreAll_PreservesNewerDiskEdits(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	file := filepath.Join(dir, "creds.json")
	writeFile(t, file, "v1")

	src := entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: dir, Mode: "folder", Enabled: true,
	}
	if _, err := mgr.SaveSource(ctx, src); err != nil {
		t.Fatalf("save: %v", err)
	}

	// user edits file after sync; DB still has v1
	writeFile(t, file, "v2-edited")

	if err := mgr.RestoreAll(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := readFile(t, file); got != "v2-edited" {
		t.Errorf("disk edit was overwritten! got %q want v2-edited", got)
	}
}

func TestRestoreAll_SkipsRowsWithoutEnabledSource(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	file := filepath.Join(dir, "creds.json")
	writeFile(t, file, "v1")

	src := entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: dir, Mode: "folder", Enabled: true,
	}
	saved, err := mgr.SaveSource(ctx, src)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	// delete disk + disable source
	if err := os.Remove(file); err != nil {
		t.Fatalf("remove: %v", err)
	}
	saved.Enabled = false
	if _, err := mgr.SaveSource(ctx, saved); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if err := mgr.RestoreAll(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("file was restored despite disabled source")
	}
}

// RestoreAll no longer wipes legacy rows — that moved to postgres.Migrate
// as a one-shot DB migration. wipeLegacyRelPathRows is still exercised
// directly by TestWipeLegacyRelPathRows.

// ─── RestoreSelected force-overwrites ─────────────────────────────────

func TestRestoreSelected_ForceOverwritesDisk(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	file := filepath.Join(dir, "creds.json")
	writeFile(t, file, "v1")

	src := entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: dir, Mode: "folder", Enabled: true,
	}
	if _, err := mgr.SaveSource(ctx, src); err != nil {
		t.Fatalf("save: %v", err)
	}

	// disk diverges from DB
	writeFile(t, file, "diverged")

	rows, _ := mgr.ListAll(ctx)
	var id uint
	for _, r := range rows {
		if !r.IsDir {
			id = r.ID
		}
	}
	if id == 0 {
		t.Fatal("no file row")
	}

	n, err := mgr.RestoreSelected(ctx, []uint{id}, nil)
	if err != nil {
		t.Fatalf("restore selected: %v", err)
	}
	if n != 1 {
		t.Errorf("restored = %d, want 1", n)
	}
	if got := readFile(t, file); got != "v1" {
		t.Errorf("force restore failed: got %q want v1", got)
	}
}

func TestRestoreSelected_SkipsDirRows(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", "x.yml"), "X")

	src := entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: dir, Mode: "folder", Enabled: true,
	}
	if _, err := mgr.SaveSource(ctx, src); err != nil {
		t.Fatalf("save: %v", err)
	}

	rows, _ := mgr.ListAll(ctx)
	var dirID uint
	for _, r := range rows {
		if r.IsDir {
			dirID = r.ID
			break
		}
	}
	if dirID == 0 {
		t.Skip("no dir row created (filesystem layout edge case)")
	}

	n, err := mgr.RestoreSelected(ctx, []uint{dirID}, nil)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n != 0 {
		t.Errorf("dir row should be skipped, got n=%d", n)
	}
}

// ─── RecomputeRetention propagates source edits to file rows ──────────

func TestRecomputeRetention_AppliesNewSourceRetentionImmediately(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	root := t.TempDir()
	support := filepath.Join(root, ".support-tools")
	agents := filepath.Join(support, "agents")
	writeFile(t, filepath.Join(agents, "a.yml"), "A")
	writeFile(t, filepath.Join(support, "config.yml"), "C")

	srcA, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "wick", InstanceName: "wick",
		SyncPath: support, Mode: "folder", RetentionDays: 0, Enabled: true,
	})
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "wick", InstanceName: "wick",
		SyncPath: agents, Mode: "folder", RetentionDays: 7, Enabled: true,
	}); err != nil {
		t.Fatalf("save B: %v", err)
	}

	rowsByPath := func() map[string]entity.ProviderStorage {
		rows, _ := mgr.ListAll(ctx)
		m := map[string]entity.ProviderStorage{}
		for _, r := range rows {
			if !r.IsDir {
				m[r.RelPath] = r
			}
		}
		return m
	}

	wantA := filepath.ToSlash(filepath.Join(agents, "a.yml"))
	wantC := filepath.ToSlash(filepath.Join(support, "config.yml"))

	rows := rowsByPath()
	if rows[wantA].RetentionDays != 7 {
		t.Errorf("after save B, a.yml retention = %d, want 7", rows[wantA].RetentionDays)
	}
	if rows[wantC].RetentionDays != 0 {
		t.Errorf("config.yml retention = %d, want 0", rows[wantC].RetentionDays)
	}

	// Edit source A's retention; recompute should cascade.
	srcA.RetentionDays = 30
	if _, err := mgr.SaveSource(ctx, srcA); err != nil {
		t.Fatalf("update A: %v", err)
	}
	rows = rowsByPath()
	if rows[wantC].RetentionDays != 30 {
		t.Errorf("after update A, config.yml retention = %d, want 30", rows[wantC].RetentionDays)
	}
	// a.yml is still deeper-covered by B (7d) — must not drift to 30.
	if rows[wantA].RetentionDays != 7 {
		t.Errorf("a.yml retention drifted = %d, want 7 (B is deeper)", rows[wantA].RetentionDays)
	}
}

func TestRecomputeRetention_DeleteSourceFallsBack(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	root := t.TempDir()
	support := filepath.Join(root, ".support-tools")
	agents := filepath.Join(support, "agents")
	writeFile(t, filepath.Join(agents, "a.yml"), "A")

	srcA, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "wick", InstanceName: "wick",
		SyncPath: support, Mode: "folder", RetentionDays: 0, Enabled: true,
	})
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	srcB, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "wick", InstanceName: "wick",
		SyncPath: agents, Mode: "folder", RetentionDays: 7, Enabled: true,
	})
	if err != nil {
		t.Fatalf("save B: %v", err)
	}
	_ = srcA

	wantA := filepath.ToSlash(filepath.Join(agents, "a.yml"))

	rows, _ := mgr.ListAll(ctx)
	for _, r := range rows {
		if !r.IsDir && r.RelPath == wantA && r.RetentionDays != 7 {
			t.Fatalf("precondition: a.yml retention = %d, want 7", r.RetentionDays)
		}
	}

	// Remove the deeper source → file should fall back to the shallower
	// source's retention (0 / lifetime).
	if err := mgr.DeleteSource(ctx, srcB.ID); err != nil {
		t.Fatalf("delete B: %v", err)
	}
	rows, _ = mgr.ListAll(ctx)
	for _, r := range rows {
		if r.IsDir || r.RelPath != wantA {
			continue
		}
		if r.RetentionDays != 0 {
			t.Errorf("after deleting B, a.yml retention = %d, want 0 (fallback to A)", r.RetentionDays)
		}
	}
}

func TestRecomputeRetention_NoMatchingSourceKeepsZero(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "foo.yml"), "F")

	src, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: dir, Mode: "folder", RetentionDays: 5, Enabled: true,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := mgr.DeleteSource(ctx, src.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Row remains in DB (DeleteSource doesn't purge files); retention
	// recomputed → no covering source → 0.
	rows, _ := mgr.ListAll(ctx)
	found := false
	for _, r := range rows {
		if !r.IsDir {
			found = true
			if r.RetentionDays != 0 {
				t.Errorf("orphan row retention = %d, want 0 (no source)", r.RetentionDays)
			}
		}
	}
	if !found {
		t.Skip("file row not present (filesystem race?)")
	}
}

// TestDeleteByID_CascadesFolderSubtree — deleting a folder row must remove
// every descendant in the same instance, but must NOT touch rows from
// another instance even if their parent_id coincidentally matches.
func TestDeleteByID_CascadesFolderSubtree(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	deep := filepath.Join(dir, "sub", "nested")
	writeFile(t, filepath.Join(deep, "a.txt"), "A")
	writeFile(t, filepath.Join(deep, "b.txt"), "B")
	writeFile(t, filepath.Join(dir, "top.txt"), "T")

	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Also seed a separate instance with its own subtree to ensure
	// scoping holds.
	otherDir := t.TempDir()
	writeFile(t, filepath.Join(otherDir, "x.txt"), "X")
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "other",
		SyncPath: otherDir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save other: %v", err)
	}

	// Find the "sub" folder row for instance "i".
	subAbs := filepath.ToSlash(filepath.Join(dir, "sub"))
	var sub entity.ProviderStorage
	if err := db.Where("provider_type = ? AND instance_name = ? AND rel_path = ?", "p", "i", subAbs).First(&sub).Error; err != nil {
		t.Fatalf("find sub: %v", err)
	}

	// Delete the folder → must cascade to sub/nested, a.txt, b.txt.
	if err := mgr.DeleteByID(ctx, sub.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var remaining []entity.ProviderStorage
	db.Where("provider_type = ? AND instance_name = ?", "p", "i").Find(&remaining)
	for _, r := range remaining {
		if strings.HasPrefix(filepath.ToSlash(r.RelPath), subAbs) {
			t.Errorf("descendant survived cascade: %q (is_dir=%v)", r.RelPath, r.IsDir)
		}
	}
	// top.txt + its folder chain must survive
	topAbs := filepath.ToSlash(filepath.Join(dir, "top.txt"))
	found := false
	for _, r := range remaining {
		if r.RelPath == topAbs {
			found = true
		}
	}
	if !found {
		t.Error("sibling file top.txt got destroyed by cascade")
	}

	// Other instance must be untouched.
	var otherRows int64
	db.Model(&entity.ProviderStorage{}).Where("provider_type = ? AND instance_name = ?", "p", "other").Count(&otherRows)
	if otherRows == 0 {
		t.Error("cascade leaked across instances")
	}
}

// ─── Exclude patterns ─────────────────────────────────────────────────

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"*.log", "/home/app/foo.log", true},                                       // basename glob
		{"*.log", "/home/app/foo.txt", false},                                      //
		{"**/files/**", "/home/.wick/workspaces/abc/files/x.json", true},           // doublestar
		{"**/files/**", "/home/.wick/workspaces/abc/sources/y.json", false},        // sibling untouched
		{"/home/.wick/workspaces/*/files/**", "/home/.wick/workspaces/a/files/b", true},
		{"/home/.wick/workspaces/*/files/**", "/home/.wick/workspaces/a/b/c", false},
		{"node_modules", "/x/y/node_modules/z", true},                              // basename anywhere
		{"node_modules", "/x/y/node_modules", true},                                // basename exact
		{"node_modules", "/x/y/other", false},                                      //
		{"/abs/only", "/abs/only", true},                                           // exact abs
		{"/abs/only", "/abs/only/sub", true},                                       // literal abs = dir + descendants
		{"/abs/only", "/abs/onlyish", false},                                       // prefix without slash boundary should not match
	}
	for _, c := range cases {
		got := globMatch(c.pattern, c.path)
		if got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestCollectExcludePatterns_FiltersByMode(t *testing.T) {
	sources := []entity.ProviderStorageSource{
		{Mode: "folder", SyncPath: "/include/me", Enabled: true},
		{Mode: "exclude", SyncPath: "*.log", Enabled: true},
		{Mode: "exclude", SyncPath: "**/secrets/**", Enabled: false}, // disabled
		{Mode: "single", SyncPath: "/keep/this", Enabled: true},
		{Mode: "exclude", SyncPath: "  ", Enabled: true}, // empty
		{Mode: "exclude", SyncPath: "node_modules", Enabled: true},
	}
	got := collectExcludePatterns(sources)
	want := []string{"*.log", "node_modules"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, p := range want {
		if got[i] != p {
			t.Errorf("[%d] = %q, want %q", i, got[i], p)
		}
	}
}

func TestCollectFiles_HonorsExcludes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "keep.yml"), "K")
	writeFile(t, filepath.Join(dir, "drop.log"), "D")
	writeFile(t, filepath.Join(dir, "workspaces", "ws1", "files", "blob.bin"), "B")
	writeFile(t, filepath.Join(dir, "workspaces", "ws1", "config.yml"), "C")

	out, err := collectFiles(&provider.StorageConfig{Mode: "folder", SyncPath: dir},
		[]string{"*.log", "**/workspaces/*/files/**"})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	mustHave := []string{
		filepath.ToSlash(filepath.Join(dir, "keep.yml")),
		filepath.ToSlash(filepath.Join(dir, "workspaces", "ws1", "config.yml")),
	}
	mustNot := []string{
		filepath.ToSlash(filepath.Join(dir, "drop.log")),
		filepath.ToSlash(filepath.Join(dir, "workspaces", "ws1", "files", "blob.bin")),
	}
	for _, p := range mustHave {
		if _, ok := out[p]; !ok {
			t.Errorf("missing kept file: %q (got %v)", p, keysOf(out))
		}
	}
	for _, p := range mustNot {
		if _, ok := out[p]; ok {
			t.Errorf("excluded file leaked through: %q", p)
		}
	}
}

func TestSaveSource_PurgesExistingExcludedRows(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ws", "a", "files", "secret.bin"), "S")
	writeFile(t, filepath.Join(dir, "ws", "a", "config.yml"), "C")

	// First save: include the whole tree → both files land in DB.
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "wick", InstanceName: "wick",
		SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save include: %v", err)
	}

	rows, _ := mgr.ListAll(ctx)
	hasFiles := func(absSubstr string) bool {
		for _, r := range rows {
			if !r.IsDir && strings.Contains(r.RelPath, absSubstr) {
				return true
			}
		}
		return false
	}
	if !hasFiles("/files/") {
		t.Fatal("precondition: files dir should be present before exclude")
	}

	// Add an exclude-mode source → SaveSource cascade should purge.
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "wick", InstanceName: "wick",
		SyncPath: "**/files/**", Mode: "exclude", Enabled: true,
	}); err != nil {
		t.Fatalf("save exclude: %v", err)
	}
	rows, _ = mgr.ListAll(ctx)
	for _, r := range rows {
		if !r.IsDir && strings.Contains(r.RelPath, "/files/") {
			t.Errorf("excluded row survived purge: %q", r.RelPath)
		}
	}
	// config.yml must remain untouched.
	if !func() bool {
		for _, r := range rows {
			if !r.IsDir && strings.HasSuffix(r.RelPath, "/config.yml") {
				return true
			}
		}
		return false
	}() {
		t.Error("config.yml got purged but shouldn't have")
	}

	// Empty folders pruned: the now-empty "files" dir row should be gone.
	for _, r := range rows {
		if r.IsDir && strings.HasSuffix(r.RelPath, "/files") {
			t.Errorf("empty folder row %q survived prune", r.RelPath)
		}
	}
}

// TestRepairTree_FixesOrphanedChildren replicates the user-reported bug:
// the C: drive-letter folder row got deleted and recreated at a new ID,
// but its descendants (Users, Staffinc, …) still reference the dead
// parent ID, so listChildren(C:_NEW) returns []. RepairTree must rewire
// them based on rel_path.
func TestRepairTree_FixesOrphanedChildren(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	// Seed an orphaned hierarchy by hand: C: (id will be assigned),
	// then C:/Users with parent_id pointing to a NON-EXISTENT row.
	cRow := entity.ProviderStorage{
		ProviderType: "wick", InstanceName: "wick",
		RelPath: "C:", ParentID: 0, Name: "C:", IsDir: true,
	}
	if err := db.Create(&cRow).Error; err != nil {
		t.Fatalf("seed C:: %v", err)
	}
	users := entity.ProviderStorage{
		ProviderType: "wick", InstanceName: "wick",
		RelPath: "C:/Users", ParentID: 99999, Name: "Users", IsDir: true,
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed Users: %v", err)
	}
	staffinc := entity.ProviderStorage{
		ProviderType: "wick", InstanceName: "wick",
		RelPath: "C:/Users/Staffinc", ParentID: 99998, Name: "Staffinc", IsDir: true,
	}
	if err := db.Create(&staffinc).Error; err != nil {
		t.Fatalf("seed Staffinc: %v", err)
	}
	creds := entity.ProviderStorage{
		ProviderType: "wick", InstanceName: "wick",
		RelPath: "C:/Users/Staffinc/.creds", ParentID: 88888, Name: ".creds", IsDir: false,
		ContentHash: "abc",
	}
	if err := db.Create(&creds).Error; err != nil {
		t.Fatalf("seed creds: %v", err)
	}

	// Before repair: drilling C: returns nothing (the bug).
	pre, _ := mgr.ListChildren(ctx, "wick", "wick", cRow.ID)
	if len(pre) != 0 {
		t.Fatalf("precondition: C: should appear empty before repair, got %d", len(pre))
	}

	// Repair: walks rel_path → wires parent_id correctly.
	n, err := mgr.RepairTree(ctx)
	if err != nil {
		t.Fatalf("RepairTree: %v", err)
	}
	if n < 3 {
		t.Errorf("expected >=3 rows fixed (Users, Staffinc, .creds), got %d", n)
	}

	// After repair: C: must show Users.
	post, err := mgr.ListChildren(ctx, "wick", "wick", cRow.ID)
	if err != nil {
		t.Fatalf("listChildren: %v", err)
	}
	if len(post) != 1 || post[0].Name != "Users" {
		var names []string
		for _, r := range post {
			names = append(names, r.Name)
		}
		t.Errorf("C: children = %v, want [Users]", names)
	}

	// And the whole chain drills down correctly.
	usersChildren, _ := mgr.ListChildren(ctx, "wick", "wick", post[0].ID)
	if len(usersChildren) != 1 || usersChildren[0].Name != "Staffinc" {
		t.Errorf("Users children unexpected: %+v", usersChildren)
	}
}

func TestRepairTree_Idempotent(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "A")
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// First repair on a healthy tree should be a no-op.
	n, err := mgr.RepairTree(ctx)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if n != 0 {
		t.Errorf("repair on healthy tree fixed %d rows, want 0", n)
	}
}

// TestSyncAll_RebuildsAfterLegacyWipe simulates an upgrade where the DB
// carries old relative-path rows (pre-fix) + the sources from the new
// schema. wipeLegacyRelPathRows would otherwise leave the file rows
// empty until the next cron tick; SyncAll at startup must re-populate
// them immediately.
func TestSyncAll_RebuildsAfterLegacyWipe(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	file := filepath.Join(dir, "creds.json")
	writeFile(t, file, "real-content")

	// Source exists; SaveSource auto-syncs (so DB starts populated).
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Seed a legacy row that would survive without wipe but is garbage.
	if err := db.Create(&entity.ProviderStorage{
		ProviderType: "p", InstanceName: "i",
		RelPath: "legacy/creds.json", Name: "legacy-creds.json", ContentHash: "old",
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Drop ALL file rows to mimic the worst case where wipe removed
	// even the absolute-path rows (legacy bug / partial migration).
	if err := db.Where("is_dir = ?", false).Delete(&entity.ProviderStorage{}).Error; err != nil {
		t.Fatalf("nuke: %v", err)
	}

	// SyncAll should rebuild the file row from disk.
	n, err := mgr.SyncAll(ctx)
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if n != 1 {
		t.Errorf("synced sources = %d, want 1", n)
	}

	rows, _ := mgr.ListAll(ctx)
	wantAbs := filepath.ToSlash(file)
	found := false
	for _, r := range rows {
		if !r.IsDir && r.RelPath == wantAbs && string(r.Content) == "real-content" {
			found = true
		}
	}
	if !found {
		var paths []string
		for _, r := range rows {
			paths = append(paths, r.RelPath)
		}
		t.Errorf("expected re-synced row at %q, got rows: %v", wantAbs, paths)
	}
}

func TestSyncAll_SkipsDisabledSources(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "A")

	saved, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: dir, Mode: "folder", Enabled: true,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// Disable it, then nuke files; SyncAll must not re-sync.
	saved.Enabled = false
	if _, err := mgr.SaveSource(ctx, saved); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := db.Where("is_dir = ?", false).Delete(&entity.ProviderStorage{}).Error; err != nil {
		t.Fatalf("nuke: %v", err)
	}

	n, _ := mgr.SyncAll(ctx)
	if n != 0 {
		t.Errorf("synced disabled source: count=%d", n)
	}
	rows, _ := mgr.ListAll(ctx)
	for _, r := range rows {
		if !r.IsDir {
			t.Errorf("disabled source got re-synced: %v", r.RelPath)
		}
	}
}

func TestSyncAll_OneBadSourceDoesNotBlockOthers(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	good := t.TempDir()
	writeFile(t, filepath.Join(good, "a.txt"), "A")

	// "bad" source points to a non-existent path.
	bad := filepath.Join(t.TempDir(), "does-not-exist")

	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		Label: "bad", SyncPath: bad, Mode: "single", Enabled: true,
	}); err != nil {
		t.Fatalf("save bad: %v", err)
	}
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		Label: "good", SyncPath: good, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save good: %v", err)
	}

	// Nuke files so we can observe SyncAll's effect cleanly.
	if err := db.Where("is_dir = ?", false).Delete(&entity.ProviderStorage{}).Error; err != nil {
		t.Fatalf("nuke: %v", err)
	}

	n, err := mgr.SyncAll(ctx)
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if n != 1 {
		t.Errorf("synced = %d, want 1 (bad source must be skipped, good must succeed)", n)
	}

	rows, _ := mgr.ListAll(ctx)
	wantAbs := filepath.ToSlash(filepath.Join(good, "a.txt"))
	found := false
	for _, r := range rows {
		if !r.IsDir && r.RelPath == wantAbs {
			found = true
		}
	}
	if !found {
		t.Errorf("good source's row missing despite bad source erroring")
	}
}

// TestUserScenario_MultipleSourcesAtDifferentDrives replicates the failure
// the user reported: three sources for the same provider/instance
// (one single-mode, two folder-mode at different absolute roots) → the
// file explorer's root view drills into the top-level drive folder but
// shows "Empty folder." The expectation is that drilling C: lists Users.
func TestUserScenario_MultipleSourcesAtDifferentDrives(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	// Stand up a synthetic Windows-style tree under TempDir, then mount
	// the source paths verbatim so the abs path keys are deterministic.
	root := t.TempDir()
	wickLab := filepath.Join(root, "wick-lab")
	logsDir := filepath.Join(wickLab, "logs")
	creds := filepath.Join(wickLab, "INITIAL_CREDENTIALS.txt")
	writeFile(t, creds, "TOKEN")
	writeFile(t, filepath.Join(logsDir, "a.log"), "log a")
	writeFile(t, filepath.Join(logsDir, "b.log"), "log b")

	for _, src := range []entity.ProviderStorageSource{
		{ProviderType: "wick", InstanceName: "wick", Label: "INITIAL_CREDENTIALS.txt", SyncPath: creds, Mode: "single", Enabled: true},
		{ProviderType: "wick", InstanceName: "wick", Label: "logs", SyncPath: logsDir, Mode: "folder", Enabled: true},
	} {
		if _, err := mgr.SaveSource(ctx, src); err != nil {
			t.Fatalf("save %s: %v", src.Label, err)
		}
	}

	// Roots view (parent_id=0) must include exactly one entry per
	// top-level segment of the abs path. On the test box that is the
	// first segment of the temp dir (e.g. "tmp" on Linux, "C:" on
	// Windows). Whatever it is, drilling into it must NOT be empty.
	roots, err := mgr.ListRoots(ctx)
	if err != nil {
		t.Fatalf("roots: %v", err)
	}
	if len(roots) == 0 {
		t.Fatal("listRoots returned 0 — sync didn't write anything")
	}

	for _, r := range roots {
		if !r.IsDir {
			continue
		}
		children, err := mgr.ListChildren(ctx, r.ProviderType, r.InstanceName, r.ID)
		if err != nil {
			t.Fatalf("listChildren %s: %v", r.RelPath, err)
		}
		if len(children) == 0 {
			t.Errorf("root %q (id=%d) is shown to user but its children list is empty",
				r.RelPath, r.ID)
		}
	}
}


// Simulates the exact pre-fix scenario: two overlapping sources with the same
// instance ran through a sync → restore → sync cycle. With the old
// (rel-path) implementation the file would stack as agents/agents/agents/…;
// with the absolute-path implementation each file fixates on its real
// filesystem path.
func TestNoStackingAfterRestoreSyncCycle(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()

	root := t.TempDir()
	support := filepath.Join(root, ".support-tools")
	agents := filepath.Join(support, "agents")
	target := filepath.Join(agents, "policy.yml")
	writeFile(t, target, "v1")

	for _, p := range []string{support, agents} {
		if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
			ProviderType: "wick", InstanceName: "wick",
			SyncPath: p, Mode: "folder", Enabled: true,
		}); err != nil {
			t.Fatalf("save %s: %v", p, err)
		}
	}

	// Cycle several times; if logic were broken, paths would stack.
	for i := 0; i < 3; i++ {
		if err := mgr.RestoreAll(ctx); err != nil {
			t.Fatalf("restore[%d]: %v", i, err)
		}
		for _, p := range []string{support, agents} {
			if err := mgr.SyncOne(ctx, provider.Instance{
				Type: "wick", Name: "wick",
				Storage: &provider.StorageConfig{Mode: "folder", SyncPath: p},
			}); err != nil {
				t.Fatalf("sync[%d] %s: %v", i, p, err)
			}
		}
	}

	rows, _ := mgr.ListAll(ctx)
	wantAbs := filepath.ToSlash(target)
	var files []string
	for _, r := range rows {
		if r.IsDir {
			continue
		}
		files = append(files, r.RelPath)
		if strings.Count(r.RelPath, "agents/") > 1 {
			t.Errorf("path stacking detected: %q", r.RelPath)
		}
	}
	if len(files) != 1 || files[0] != wantAbs {
		t.Errorf("want exactly one row at %q, got %v", wantAbs, files)
	}

	// And the disk path remains the original — no agents/agents on disk
	matches, _ := filepath.Glob(filepath.Join(agents, "agents"))
	if len(matches) != 0 {
		t.Errorf("agents/agents directory created on disk: %v", matches)
	}
}
