package providersync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
)

// ─── String-only path tests (run on any OS) ───────────────────────────

func TestGlobMatch_WindowsBackslashPattern(t *testing.T) {
	// User on Windows enters or stores a path with backslashes; matcher
	// must ToSlash both sides so a stored exclude pattern still matches
	// the absolute rel_path (which is always stored as forward slash).
	pattern := `C:\Users\Staffinc\.wick-lab\logs`
	path := "C:/Users/Staffinc/.wick-lab/logs/gate-2026-05-13.log"
	if !globMatch(pattern, path) {
		t.Errorf("backslash pattern should match forward-slash path")
	}
}

func TestGlobMatch_TrailingSlashPattern(t *testing.T) {
	// path/filepath.Clean strips trailing slashes; ensure callers that
	// pass an un-cleaned pattern (e.g. directly from user input) still
	// behave sanely.
	if !globMatch("/home/app/logs/", "/home/app/logs/x.log") {
		t.Errorf("pattern with trailing slash should still match descendants")
	}
}

func TestGlobMatch_CaseSensitivity(t *testing.T) {
	// The matcher is byte-for-byte. macOS/Windows filesystems are
	// case-insensitive but the abs-path key in DB is whatever the OS
	// reports; sync stays consistent because the same OS reports the
	// same casing every time. Documented as exact-match here.
	if globMatch("/Home/app", "/home/app") {
		t.Errorf("matcher is case-sensitive by design")
	}
	if !globMatch("/home/app", "/home/app") {
		t.Errorf("exact-case must match")
	}
}

func TestGlobMatch_SpecialRegexChars(t *testing.T) {
	// File / folder names commonly contain '.', '+', '(', etc. These
	// must be regex-escaped in globRegex so the pattern matches literally.
	for _, c := range []struct{ pat, path string }{
		{"/home/.config/x.yml", "/home/.config/x.yml"},
		{"/home/app(v2)/data", "/home/app(v2)/data/file"},
		{"/home/a+b/c", "/home/a+b/c/x"},
		{"/home/$DATA/x", "/home/$DATA/x"},
		{"/home/{tmp}/y", "/home/{tmp}/y"},
		{"/home/foo[1]/x", "/home/foo[1]/x"},
	} {
		if !globMatch(c.pat, c.path) {
			t.Errorf("special-char literal must match: pattern=%q path=%q", c.pat, c.path)
		}
	}
}

func TestEnsureFolderChain_MultipleConsecutiveSlashes(t *testing.T) {
	// Defensive: input like "/home//app/foo.yml" shouldn't create an
	// empty-name folder row between the slashes.
	db := newDB(t)
	s := newStore(db)
	if _, err := s.ensureFolderChain(context.Background(), "p", "i", "/home//app/foo.yml"); err != nil {
		t.Fatalf("chain: %v", err)
	}
	var rows []entity.ProviderStorage
	db.Where("is_dir = ?", true).Find(&rows)
	for _, r := range rows {
		if r.Name == "" {
			t.Errorf("empty-name folder row created from double-slash: %+v", r)
		}
	}
}

func TestEnsureFolderChain_PathWithSpaces(t *testing.T) {
	db := newDB(t)
	s := newStore(db)
	parent, err := s.ensureFolderChain(context.Background(), "p", "i", "/home/My Apps/.config/app.json")
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	if parent == 0 {
		t.Fatal("expected non-zero parent for nested file")
	}
	var n int64
	db.Model(&entity.ProviderStorage{}).
		Where("is_dir = ? AND rel_path = ?", true, "/home/My Apps").Count(&n)
	if n != 1 {
		t.Errorf("expected folder row %q to exist, count=%d", "/home/My Apps", n)
	}
}

func TestEnsureFolderChain_UnicodePath(t *testing.T) {
	db := newDB(t)
	s := newStore(db)
	abs := "/home/プロジェクト/.cfg/データ.yml"
	parent, err := s.ensureFolderChain(context.Background(), "p", "i", abs)
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	if parent == 0 {
		t.Fatal("expected non-zero parent for nested unicode file")
	}
	var n int64
	db.Model(&entity.ProviderStorage{}).
		Where("is_dir = ? AND rel_path = ?", true, "/home/プロジェクト").Count(&n)
	if n != 1 {
		t.Errorf("unicode folder row missing")
	}
}

func TestWipeLegacyRelPathRows_HandlesUnicodeAndSpaces(t *testing.T) {
	db := newDB(t)
	s := newStore(db)
	seeds := []entity.ProviderStorage{
		{ProviderType: "p", InstanceName: "i", RelPath: "プロジェクト/foo", Name: "a", ContentHash: "x"}, // legacy
		{ProviderType: "p", InstanceName: "i", RelPath: "/home/My Apps/x", Name: "b", ContentHash: "y"}, // abs
		{ProviderType: "p", InstanceName: "i", RelPath: "C:/プロジェクト/x", Name: "c", ContentHash: "z"},   // abs win
	}
	for _, row := range seeds {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := s.wipeLegacyRelPathRows(context.Background()); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	var rows []entity.ProviderStorage
	db.Order("rel_path").Find(&rows)
	got := pathsOf(rows)
	want := []string{"/home/My Apps/x", "C:/プロジェクト/x"}
	if len(got) != len(want) {
		t.Fatalf("survivors = %v, want %v", got, want)
	}
}

// ─── Real-disk tests using TempDir (cross-platform) ────────────────────

func TestSync_PathWithSpaces_OnDisk(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My Apps", "data")
	writeFile(t, filepath.Join(dir, "config a.json"), "spaced")

	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, _ := mgr.ListAll(ctx)
	want := filepath.ToSlash(filepath.Join(dir, "config a.json"))
	found := false
	for _, r := range rows {
		if !r.IsDir && r.RelPath == want && string(r.Content) == "spaced" {
			found = true
		}
	}
	if !found {
		t.Errorf("file with space in path/name not synced: want %q in %v", want, pathsOf(rows))
	}
}

func TestSync_UnicodeFilenames_OnDisk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "プロジェクト.yml"), "u")
	writeFile(t, filepath.Join(dir, "emoji-📦.txt"), "e")

	db := newDB(t)
	mgr := New(db)
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, _ := mgr.ListAll(context.Background())
	wantU := filepath.ToSlash(filepath.Join(dir, "プロジェクト.yml"))
	wantE := filepath.ToSlash(filepath.Join(dir, "emoji-📦.txt"))
	have := map[string]bool{}
	for _, r := range rows {
		have[r.RelPath] = true
	}
	if !have[wantU] || !have[wantE] {
		t.Errorf("unicode/emoji filenames missing: have %v", have)
	}
}

func TestSync_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "empty.yml"), "")
	db := newDB(t)
	mgr := New(db)
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, _ := mgr.ListAll(context.Background())
	for _, r := range rows {
		if !r.IsDir && filepath.Base(r.RelPath) == "empty.yml" {
			if len(r.Content) != 0 {
				t.Errorf("empty file got %d bytes", len(r.Content))
			}
			return
		}
	}
	t.Error("empty file row missing")
}

func TestSync_BinaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "image.bin")
	binary := []byte{0x00, 0xFF, 0x10, 0x20, 0xAB, 0xCD, 0x00, 0x80}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, binary, 0o644); err != nil {
		t.Fatal(err)
	}
	db := newDB(t)
	mgr := New(db)
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, _ := mgr.ListAll(context.Background())
	for _, r := range rows {
		if !r.IsDir && filepath.Base(r.RelPath) == "image.bin" {
			if string(r.Content) != string(binary) {
				t.Errorf("binary content corrupted")
			}
			return
		}
	}
	t.Error("binary file row missing")
}

func TestSync_LargeTree_AllFilesPresent(t *testing.T) {
	dir := t.TempDir()
	const N = 50
	for i := 0; i < N; i++ {
		sub := fmt.Sprintf("level%d", i%5)
		writeFile(t, filepath.Join(dir, sub, fmt.Sprintf("f%03d.txt", i)), fmt.Sprintf("c%d", i))
	}
	db := newDB(t)
	mgr := New(db)
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, _ := mgr.ListAll(context.Background())
	files := 0
	for _, r := range rows {
		if !r.IsDir {
			files++
		}
	}
	if files != N {
		t.Errorf("got %d file rows, want %d", files, N)
	}
}

func TestSync_NonExistentSourcePath_DoesNotPanic(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ghost := filepath.Join(t.TempDir(), "does-not-exist")
	// SaveSource auto-triggers SyncOne which surfaces the missing-path
	// error via log only — must NOT propagate to caller.
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: ghost, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Verify the source row landed even though sync failed.
	srcs, _ := mgr.ListSources(context.Background())
	if len(srcs) != 1 {
		t.Errorf("source row missing despite sync failure: %d", len(srcs))
	}
}

func TestSync_SingleModeOnMissingFile(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ghost := filepath.Join(t.TempDir(), "ghost.json")
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: ghost, Mode: "single", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	srcs, _ := mgr.ListSources(context.Background())
	if len(srcs) != 1 {
		t.Errorf("single-mode source row missing on bad path")
	}
}

func TestRestoreAll_CreatesMissingParentDir(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "creds.json")
	writeFile(t, deep, "secret")

	db := newDB(t)
	mgr := New(db)
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Wipe disk entirely; restore must mkdir the chain.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RestoreAll(context.Background()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if readFile(t, deep) != "secret" {
		t.Errorf("file not restored into freshly-created dir tree")
	}
}

// ─── Concurrency ──────────────────────────────────────────────────────

func TestConcurrent_SyncOne_NoDuplicateRows(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), fmt.Sprintf("c%d", i))
	}
	db := newDB(t)
	mgr := New(db)
	saved, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	ins := SourceToInstance(saved)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mgr.SyncOne(context.Background(), ins)
		}()
	}
	wg.Wait()

	rows, _ := mgr.ListAll(context.Background())
	seen := map[string]int{}
	for _, r := range rows {
		if r.IsDir {
			continue
		}
		seen[r.RelPath]++
	}
	for p, n := range seen {
		if n != 1 {
			t.Errorf("duplicate row for %q: count=%d (rel_path unique index should prevent this)", p, n)
		}
	}
	if len(seen) != 10 {
		t.Errorf("expected 10 distinct file rows, got %d", len(seen))
	}
}

func TestConcurrent_SaveSourceAndSyncOne(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "race.txt"), "R")
	db := newDB(t)
	mgr := New(db)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
				ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
			})
		}()
		go func() {
			defer wg.Done()
			_ = mgr.SyncOne(context.Background(), provider.Instance{
				Type: "p", Name: "i",
				Storage: &provider.StorageConfig{Mode: "folder", SyncPath: dir},
			})
		}()
	}
	wg.Wait()

	rows, _ := mgr.ListAll(context.Background())
	files := 0
	for _, r := range rows {
		if !r.IsDir {
			files++
		}
	}
	if files != 1 {
		t.Errorf("race produced %d rows, want 1", files)
	}
}

// ─── Symlink (POSIX-only) ──────────────────────────────────────────────

func TestSync_SymlinkSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin or Developer Mode on Windows; skip")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "real.yml"), "real")
	target := filepath.Join(dir, "real.yml")
	link := filepath.Join(dir, "link.yml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink in test env: %v", err)
	}
	db := newDB(t)
	mgr := New(db)
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, _ := mgr.ListAll(context.Background())
	names := []string{}
	for _, r := range rows {
		if !r.IsDir {
			names = append(names, filepath.Base(r.RelPath))
		}
	}
	// real.yml should be present; link.yml should be skipped (non-regular).
	hasReal, hasLink := false, false
	for _, n := range names {
		if n == "real.yml" {
			hasReal = true
		}
		if n == "link.yml" {
			hasLink = true
		}
	}
	if !hasReal {
		t.Errorf("regular file missing: %v", names)
	}
	if hasLink {
		t.Errorf("symlink was synced (should be skipped): %v", names)
	}
}

// ─── Linux case-sensitivity ───────────────────────────────────────────

func TestSync_CaseSensitiveFilenames_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only linux filesystems are guaranteed case-sensitive")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Foo.yml"), "upper")
	writeFile(t, filepath.Join(dir, "foo.yml"), "lower")
	db := newDB(t)
	mgr := New(db)
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, _ := mgr.ListAll(context.Background())
	files := 0
	for _, r := range rows {
		if !r.IsDir {
			files++
		}
	}
	if files != 2 {
		t.Errorf("case-sensitive FS lost one file: count=%d", files)
	}
}

// ─── Exclude edge cases ───────────────────────────────────────────────

func TestExclude_PatternBackslashStoredButStillMatches(t *testing.T) {
	// Reproduces the user-reported bug: exclude row was saved on Windows
	// with backslash SyncPath, but file rows use forward slash. The new
	// globMatch must normalise both ends so the exclude takes effect.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ignore-me.log"), "x")
	writeFile(t, filepath.Join(dir, "keep.yml"), "y")

	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Hand-craft a backslash exclude as Windows would.
	excludePath := strings.ReplaceAll(filepath.Join(dir, "ignore-me.log"), "/", `\`)
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: excludePath, Mode: "exclude", Enabled: true,
	}); err != nil {
		t.Fatalf("save exclude: %v", err)
	}

	rows, _ := mgr.ListAll(ctx)
	for _, r := range rows {
		if !r.IsDir && strings.HasSuffix(r.RelPath, "/ignore-me.log") {
			t.Errorf("excluded file survived: %q", r.RelPath)
		}
	}
}

func TestExclude_FolderPatternRemovesDescendants(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "secrets", "a.bin"), "a")
	writeFile(t, filepath.Join(dir, "secrets", "deeper", "b.bin"), "b")
	writeFile(t, filepath.Join(dir, "public", "c.yml"), "c")

	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save include: %v", err)
	}
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		SyncPath: filepath.Join(dir, "secrets"), Mode: "exclude", Enabled: true,
	}); err != nil {
		t.Fatalf("save exclude: %v", err)
	}
	rows, _ := mgr.ListAll(ctx)
	for _, r := range rows {
		if r.IsDir {
			continue
		}
		if strings.Contains(filepath.ToSlash(r.RelPath), "/secrets/") {
			t.Errorf("descendant of excluded folder survived: %q", r.RelPath)
		}
	}
	hasPublic := false
	for _, r := range rows {
		if !r.IsDir && strings.HasSuffix(r.RelPath, "/public/c.yml") {
			hasPublic = true
		}
	}
	if !hasPublic {
		t.Error("sibling file got nuked along with exclude")
	}
}

func TestExclude_DisableMode_StopsApplying(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.log"), "x")
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save inc: %v", err)
	}
	ex, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: "*.log", Mode: "exclude", Enabled: true,
	})
	if err != nil {
		t.Fatalf("save ex: %v", err)
	}
	// pre: file purged
	rows, _ := mgr.ListAll(ctx)
	for _, r := range rows {
		if !r.IsDir && strings.HasSuffix(r.RelPath, "a.log") {
			t.Fatal("precondition: a.log should be purged by exclude")
		}
	}
	// Disable the exclude → next sync re-captures.
	ex.Enabled = false
	if _, err := mgr.SaveSource(ctx, ex); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := mgr.SyncAll(ctx); err != nil {
		t.Fatalf("syncAll: %v", err)
	}
	rows, _ = mgr.ListAll(ctx)
	found := false
	for _, r := range rows {
		if !r.IsDir && strings.HasSuffix(r.RelPath, "a.log") {
			found = true
		}
	}
	if !found {
		t.Error("file should be re-captured after exclude disabled")
	}
}

// ─── Restore edge ───────────────────────────────────────────────────────

func TestRestoreAll_HandlesEmptyContentRow(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "empty.json")
	writeFile(t, target, "")
	db := newDB(t)
	mgr := New(db)
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RestoreAll(context.Background()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if readFile(t, target) != "" {
		t.Error("empty file not restored as zero-byte")
	}
}

// ─── Idempotency ───────────────────────────────────────────────────────

func TestSync_Idempotent_ManyRuns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yml"), "A")
	writeFile(t, filepath.Join(dir, "sub", "b.yml"), "B")
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	var before int64
	db.Model(&entity.ProviderStorage{}).Count(&before)
	for i := 0; i < 5; i++ {
		if _, err := mgr.SyncAll(ctx); err != nil {
			t.Fatalf("sync[%d]: %v", i, err)
		}
	}
	var after int64
	db.Model(&entity.ProviderStorage{}).Count(&after)
	if before != after {
		t.Errorf("row count drifted across repeats: %d -> %d", before, after)
	}
}

func TestRestoreAll_Idempotent_ManyRuns(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.yml")
	writeFile(t, target, "X")
	db := newDB(t)
	mgr := New(db)
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	original, _ := os.Stat(target)
	for i := 0; i < 5; i++ {
		if err := mgr.RestoreAll(context.Background()); err != nil {
			t.Fatalf("restore[%d]: %v", i, err)
		}
	}
	now, _ := os.Stat(target)
	if !now.ModTime().Equal(original.ModTime()) {
		t.Errorf("disk-wins guard failed: mtime mutated %v -> %v", original.ModTime(), now.ModTime())
	}
}

// ─── Docker-ish: volume-style mount + permission-denied path ──────────

func TestSync_PermissionDenied_DoesNotCrash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod has no POSIX permissions on Windows")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ok.yml"), "OK")
	locked := filepath.Join(dir, "locked.yml")
	if err := os.WriteFile(locked, []byte("secret"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o644) // so TempDir cleanup can remove it

	db := newDB(t)
	mgr := New(db)
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// ok.yml must still land in DB; locked.yml is silently skipped.
	rows, _ := mgr.ListAll(context.Background())
	for _, r := range rows {
		if !r.IsDir && strings.HasSuffix(r.RelPath, "ok.yml") {
			return
		}
	}
	t.Error("readable sibling missed when locked file present")
}

func TestSync_VolumeStyleMount_DifferentTempRoot(t *testing.T) {
	// Simulates the Docker case where a host volume mounts at
	// /workspace/data — abs paths shouldn't care about the prefix.
	root := t.TempDir()
	mount := filepath.Join(root, "mnt", "volume-x")
	writeFile(t, filepath.Join(mount, "a", "file.json"), "v")
	db := newDB(t)
	mgr := New(db)
	if _, err := mgr.SaveSource(context.Background(), entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i", SyncPath: mount, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, _ := mgr.ListAll(context.Background())
	want := filepath.ToSlash(filepath.Join(mount, "a", "file.json"))
	for _, r := range rows {
		if r.RelPath == want {
			return
		}
	}
	t.Errorf("mount-style abs path missing: want %q", want)
}
