package trash

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mkdirFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedStash(t *testing.T, base string) Stash {
	t.Helper()
	proj := filepath.Join(base, "projects", "acme")
	mkdirFile(t, proj, "meta.json")
	s1 := filepath.Join(base, "sessions", "s1")
	mkdirFile(t, filepath.Join(s1, "subagents", "x"), "meta.json")
	hist := filepath.Join(base, "cfg", "projects", "-acme-files")
	mkdirFile(t, hist, "chat.jsonl")
	return Stash{
		ID: "acme", Name: "Acme", OwnerUserID: "user-a", DeletedBy: "user-a", ProjectDir: proj,
		SessionDirs: map[string]string{"s1": s1, "s1--sub-x": filepath.Join(s1, "subagents", "x")},
		Providers:   map[string][]string{"claude": {hist}},
		Schedules:   []Schedule{{ID: "sch-1", Kind: "recurring", Cron: "0 9 * * *"}},
	}
}

func TestPutRestoreRoundTrip(t *testing.T) {
	base := t.TempDir()
	st := seedStash(t, base)
	m, err := Put(base, time.Now(), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Sessions) != 1 || len(m.Providers) != 1 || len(m.SessionIDs) != 2 || len(m.Schedules) != 1 {
		t.Fatalf("manifest %+v", m)
	}
	for _, p := range []string{st.ProjectDir, st.SessionDirs["s1"], st.Providers["claude"][0]} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s not moved", p)
		}
	}
	if _, err := Restore(base, m.Key); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{st.ProjectDir + "/meta.json", st.SessionDirs["s1--sub-x"] + "/meta.json", st.Providers["claude"][0] + "/chat.jsonl"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s not restored: %v", p, err)
		}
	}
	if all, _ := List(base); len(all) != 0 {
		t.Fatalf("entry left: %v", all)
	}
}

func TestRestoreRefusesOccupiedTarget(t *testing.T) {
	base := t.TempDir()
	st := seedStash(t, base)
	m, err := Put(base, time.Now(), st)
	if err != nil {
		t.Fatal(err)
	}
	mkdirFile(t, st.SessionDirs["s1"], "new.json")
	if _, err := Restore(base, m.Key); !errors.Is(err, ErrOccupied) {
		t.Fatalf("err = %v, want ErrOccupied", err)
	}
	// Nothing moved: the project folder is still in the trash.
	if _, err := os.Stat(st.ProjectDir); err == nil {
		t.Fatal("refused restore moved the project folder back")
	}
	if _, err := Load(base, m.Key); err != nil {
		t.Fatalf("entry gone after refused restore: %v", err)
	}
}

func TestPurgeOlderThanKeepsRecent(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	old := Stash{ID: "old", ProjectDir: filepath.Join(base, "projects", "old")}
	mkdirFile(t, old.ProjectDir, "meta.json")
	fresh := Stash{ID: "fresh", ProjectDir: filepath.Join(base, "projects", "fresh")}
	mkdirFile(t, fresh.ProjectDir, "meta.json")
	if _, err := Put(base, now.Add(-31*24*time.Hour), old); err != nil {
		t.Fatal(err)
	}
	if _, err := Put(base, now.Add(-29*24*time.Hour), fresh); err != nil {
		t.Fatal(err)
	}
	gone, err := PurgeOlderThan(base, now, Retention)
	if err != nil || len(gone) != 1 || gone[0].ID != "old" {
		t.Fatalf("gone %v (%v)", gone, err)
	}
	if all, _ := List(base); len(all) != 1 || all[0].ID != "fresh" {
		t.Fatalf("left %v", all)
	}
}

func TestPutMissingProjectTouchesNothing(t *testing.T) {
	base := t.TempDir()
	s1 := filepath.Join(base, "sessions", "s1")
	mkdirFile(t, s1, "meta.json")
	_, err := Put(base, time.Now(), Stash{ID: "acme", ProjectDir: filepath.Join(base, "projects", "acme"), SessionDirs: map[string]string{"s1": s1}})
	if err == nil {
		t.Fatal("Put succeeded without a project folder")
	}
	if _, err := os.Stat(s1); err != nil {
		t.Fatal("session moved although the project could not be")
	}
	if all, _ := List(base); len(all) != 0 {
		t.Fatalf("half entry left: %v", all)
	}
}

func TestLoadRejectsPathKeys(t *testing.T) {
	for _, k := range []string{"", "..", "../x", "a/b", ".hidden"} {
		if _, err := Load(t.TempDir(), k); !errors.Is(err, ErrNotFound) {
			t.Fatalf("key %q: err %v", k, err)
		}
	}
}

func TestCopyTreeFallback(t *testing.T) {
	src := filepath.Join(t.TempDir(), "a")
	mkdirFile(t, filepath.Join(src, "b"), "f.txt")
	dst := filepath.Join(t.TempDir(), "c")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "b", "f.txt")); err != nil || string(b) != "x" {
		t.Fatalf("copy %q %v", b, err)
	}
}
