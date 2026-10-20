package registry

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func newLayout(t *testing.T) config.Layout {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return layout
}

func mkProject(t *testing.T, layout config.Layout, id, name string) {
	t.Helper()
	if _, err := project.Create(layout, project.CreateOptions{ID: id, Name: name}); err != nil {
		t.Fatal(err)
	}
}

func TestReloadEmpty(t *testing.T) {
	layout := newLayout(t)
	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(r.ProjectIDs()) != 0 {
		t.Fatal("expected empty projects")
	}
	if len(r.SessionIDs()) != 0 {
		t.Fatal("expected empty sessions")
	}
}

func TestReloadAfterMutate(t *testing.T) {
	layout := newLayout(t)
	mkProject(t, layout, "p1", "p")
	_, _ = session.Create(context.Background(), layout, session.CreateOptions{ID: "S1", Origin: session.OriginUI})
	_ = preset.Create(layout, "rev", "x")

	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := r.ProjectIDs(); len(got) != 1 || got[0] != "p1" {
		t.Fatalf("projects: %v", got)
	}
	if got := r.SessionIDs(); len(got) != 1 || got[0] != "S1" {
		t.Fatalf("sessions: %v", got)
	}
	if !r.HasPreset("rev") {
		t.Fatal("preset not seen")
	}
}

func TestReloadResetsRunningStatus(t *testing.T) {
	layout := newLayout(t)
	s, _ := session.Create(context.Background(), layout, session.CreateOptions{ID: "S2", Origin: session.OriginUI})
	// Simulate a session that was mid-run when wick crashed.
	s.Meta.Status = session.StatusRunning
	_ = session.SaveMeta(layout, "S2", s.Meta)

	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Session("S2")
	if !ok {
		t.Fatal("session missing")
	}
	if got.Meta.Status != session.StatusIdle {
		t.Fatalf("status not reset: %q", got.Meta.Status)
	}
	disk, _ := session.Load(layout, "S2")
	if disk.Meta.Status != session.StatusIdle {
		t.Fatalf("disk status not reset: %q", disk.Meta.Status)
	}
}

func TestManagerCreateDelete(t *testing.T) {
	layout := newLayout(t)
	mgr, err := Bootstrap(layout)
	if err != nil {
		t.Fatal(err)
	}
	p, err := mgr.CreateProject(context.Background(), project.CreateOptions{ID: "p1", Name: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateSession(context.Background(), session.CreateOptions{ID: "S1", ProjectID: p.Meta.ID, Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Registry().ProjectIDs(); !containsName(got, "p1") {
		t.Fatalf("projects missing p1: %v", got)
	}
	if got := mgr.Registry().SessionIDs(); len(got) != 1 {
		t.Fatalf("sessions: %v", got)
	}

	if err := mgr.DeleteSession(context.Background(), "S1"); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Registry().SessionIDs(); len(got) != 0 {
		t.Fatalf("sessions after delete: %v", got)
	}

	if err := mgr.DeleteProject(context.Background(), "p1"); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Registry().ProjectIDs(); containsName(got, "p1") {
		t.Fatalf("project p1 still present after delete: %v", got)
	}
}

func TestManagerDeleteProjectRemovesSessionsAndSubAgents(t *testing.T) {
	layout := newLayout(t)
	mgr, _ := Bootstrap(layout)
	ctx := context.Background()
	_, _ = mgr.CreateProject(ctx, project.CreateOptions{ID: "p1", Name: "p"})
	_, _ = mgr.CreateProject(ctx, project.CreateOptions{ID: "p2", Name: "other"})
	_, _ = mgr.CreateSession(ctx, session.CreateOptions{ID: "S1", ProjectID: "p1", Origin: session.OriginUI})
	// A sub-agent left unscoped still goes with its parent, and so does
	// its own sub-agent.
	_, _ = mgr.CreateSession(ctx, session.CreateOptions{ID: "S1-sub", ParentSessionID: "S1", Origin: session.OriginUI})
	_, _ = mgr.CreateSession(ctx, session.CreateOptions{ID: "S1-sub-sub", ParentSessionID: "S1-sub", Origin: session.OriginUI})
	_, _ = mgr.CreateSession(ctx, session.CreateOptions{ID: "S2", ProjectID: "p2", Origin: session.OriginUI})
	_, _ = mgr.CreateSession(ctx, session.CreateOptions{ID: "S3", Origin: session.OriginUI})

	if got := mgr.ProjectSessionIDs("p1"); len(got) != 3 {
		t.Fatalf("ProjectSessionIDs = %v, want S1 + two sub-agents", got)
	}
	if err := mgr.DeleteProject(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"S1", "S1-sub", "S1-sub-sub"} {
		if _, ok := mgr.Registry().Session(sid); ok {
			t.Fatalf("session %s survived its project", sid)
		}
		if _, err := os.Stat(layout.SessionDir(sid)); !os.IsNotExist(err) {
			t.Fatalf("session dir %s still on disk: %v", sid, err)
		}
	}
	for _, sid := range []string{"S2", "S3"} {
		if _, ok := mgr.Registry().Session(sid); !ok {
			t.Fatalf("unrelated session %s was deleted", sid)
		}
	}
	if _, err := os.Stat(layout.ProjectDir("p1")); !os.IsNotExist(err) {
		t.Fatalf("project dir still on disk: %v", err)
	}
}

func TestManagerDeleteProjectKeepsCustomPath(t *testing.T) {
	layout := newLayout(t)
	mgr, _ := Bootstrap(layout)
	ctx := context.Background()
	custom := t.TempDir()
	keep := filepath.Join(custom, "user-file.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = mgr.CreateProject(ctx, project.CreateOptions{ID: "pc", Name: "custom", CustomPath: custom})
	_, _ = mgr.CreateSession(ctx, session.CreateOptions{ID: "SC", ProjectID: "pc", Origin: session.OriginUI})
	if err := mgr.DeleteProject(ctx, "pc"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("custom path file touched: %v", err)
	}
	if _, ok := mgr.Registry().Session("SC"); ok {
		t.Fatal("session of a custom-path project survived")
	}
}

func TestManagerDeleteProjectRefusesProtected(t *testing.T) {
	layout := newLayout(t)
	mgr, _ := Bootstrap(layout)
	ctx := context.Background()
	ids := mgr.Registry().ProjectIDs()
	def := ids[0]
	_, _ = mgr.CreateSession(ctx, session.CreateOptions{ID: "SD", ProjectID: def, Origin: session.OriginUI})
	if err := mgr.DeleteProject(ctx, def); err == nil {
		t.Fatal("protected project deleted")
	}
	if _, ok := mgr.Registry().Session("SD"); !ok {
		t.Fatal("refused delete still removed a session")
	}
}

func TestBootstrapIdempotent(t *testing.T) {
	layout := newLayout(t)
	if _, err := Bootstrap(layout); err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(layout); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
}

func TestBootstrapSeedsDefaultProject(t *testing.T) {
	layout := newLayout(t)
	mgr, err := Bootstrap(layout)
	if err != nil {
		t.Fatal(err)
	}
	ids := mgr.Registry().ProjectIDs()
	if len(ids) != 1 {
		t.Fatalf("expected 1 default project, got %v", ids)
	}
	p, _ := mgr.Registry().Project(ids[0])
	if p.Meta.Name != project.DefaultName {
		t.Fatalf("default project name: %q", p.Meta.Name)
	}
}

// A zero-downtime reload runs two processes over the same directory, and
// the one that did not create a session never hears about it. These
// cover the disk fallback that keeps such a session visible instead of
// being reported as deleted.

func TestSessionAdoptsFolderCreatedByAnotherProcess(t *testing.T) {
	layout := newLayout(t)
	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}

	// Stands in for the other process: writes the folder, tells us nothing.
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "S-new", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}

	if _, ok := r.Session("S-new"); !ok {
		t.Fatal("Session() reported a session that exists on disk as missing")
	}
	if !containsName(r.SessionIDs(), "S-new") {
		t.Fatal("SessionIDs() omitted a session that exists on disk")
	}
	if _, ok := r.Sessions()["S-new"]; !ok {
		t.Fatal("Sessions() omitted a session that exists on disk")
	}
}

func TestSessionStillMissingWhenFolderGone(t *testing.T) {
	layout := newLayout(t)
	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Session("never-existed"); ok {
		t.Fatal("Session() invented a session with no folder on disk")
	}
}

func TestAdoptedSessionRefreshedAndDropped(t *testing.T) {
	layout := newLayout(t)
	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "S-adopt", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Session("S-adopt"); !ok {
		t.Fatal("adoption failed")
	}

	// The owning process renames it; our cached copy must not stay stale.
	sess, err := session.Load(layout, "S-adopt")
	if err != nil {
		t.Fatal(err)
	}
	sess.Meta.Label = "renamed elsewhere"
	if err := session.SaveMeta(layout, "S-adopt", sess.Meta); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.lastDiskScan = time.Time{} // next read re-scans instead of waiting out the interval
	r.mu.Unlock()
	if got := r.Sessions()["S-adopt"].Meta.Label; got != "renamed elsewhere" {
		t.Fatalf("adopted session went stale: label = %q", got)
	}

	// And when that process deletes it, the adopted copy goes with it.
	if err := session.Delete(context.Background(), layout, "S-adopt"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.lastDiskScan = time.Time{}
	r.mu.Unlock()
	if containsName(r.SessionIDs(), "S-adopt") {
		t.Fatal("deleted session still listed after re-scan")
	}
}

// TestAdoptConcurrentReaders: the dashboard reads the session list from
// several goroutines at once. Under -race this catches both a data race in
// the scan and the rate limit being a check rather than a claim.
func TestAdoptConcurrentReaders(t *testing.T) {
	layout := newLayout(t)
	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "S-race", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = r.SessionIDs()
				_ = r.Sessions()
				_, _ = r.Session("S-race")
			}
		}()
	}
	wg.Wait()

	if !containsName(r.SessionIDs(), "S-race") {
		t.Fatal("session never became visible")
	}
	// Adopted once, not once per reader.
	if n := len(r.Sessions()); n != 1 {
		t.Fatalf("sessions = %d, want exactly 1", n)
	}
}

// TestResetSessionStatusKeepsConcurrentAgents: the startup sweep resets
// statuses on agents.json as it is now, so an entry written after s was
// loaded survives instead of being overwritten by the stale list.
func TestResetSessionStatusKeepsConcurrentAgents(t *testing.T) {
	layout := newLayout(t)
	s, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "S1", Origin: session.OriginUI})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "S1", "main", "claude"); err != nil {
		t.Fatal(err)
	}
	s, _ = session.Load(layout, "S1")
	s.Agents[0].Status = "running"
	s.Meta.Status = "running"
	// Written by someone else after s was loaded.
	if err := session.UpdateAgents(layout, "S1", func(a []session.AgentEntry) ([]session.AgentEntry, bool, error) {
		a[0].Status = "running"
		return append(a, session.AgentEntry{Name: "helper", Provider: "codex", Status: "running"}), true, nil
	}); err != nil {
		t.Fatal(err)
	}
	resetSessionStatus(layout, &s)
	got, _ := session.Load(layout, "S1")
	if len(got.Agents) != 2 {
		t.Fatalf("agents on disk = %d, want 2 (concurrent entry kept)", len(got.Agents))
	}
	for _, a := range got.Agents {
		if a.Status != "idle" {
			t.Fatalf("agent %s status %q, want idle", a.Name, a.Status)
		}
	}
	if got.Meta.Status != session.StatusIdle || s.Agents[0].Status != "idle" {
		t.Fatalf("meta=%q in-memory=%q, want idle", got.Meta.Status, s.Agents[0].Status)
	}
}

func TestProjectAdoptsFolderCreatedByAnotherProcess(t *testing.T) {
	layout := newLayout(t)
	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	// Stands in for the predecessor during a reload: writes the folder, tells us nothing.
	mkProject(t, layout, "P-new", "Client X")
	p, ok := r.Project("P-new")
	if !ok || p.Meta.Name != "Client X" {
		t.Fatalf("Project() reported a project that exists on disk as missing (ok=%v)", ok)
	}
	if _, ok := r.Project("never-existed"); ok {
		t.Fatal("Project() invented a project with no folder on disk")
	}
}

// TestResyncTakesNewerMetaFromPredecessor: a session known at boot that the
// draining predecessor then runs a turn in. The meta write reaches disk
// only; ordinary reads keep the boot copy, ResyncFromDisk takes the newer.
func TestResyncTakesNewerMetaFromPredecessor(t *testing.T) {
	layout := newLayout(t)
	for _, id := range []string{"S-turned", "S-quiet"} {
		if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: id, Origin: session.OriginUI}); err != nil {
			t.Fatal(err)
		}
	}
	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	boot := r.Sessions()["S-turned"].Meta.LastActive

	// Stands in for the predecessor answering a teammate's task.
	sess, err := session.Load(layout, "S-turned")
	if err != nil {
		t.Fatal(err)
	}
	sess.Meta.LastActive = boot.Add(48 * time.Hour)
	if err := session.SaveMeta(layout, "S-turned", sess.Meta); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.lastDiskScan = time.Time{}
	r.mu.Unlock()
	if got := r.Sessions()["S-turned"].Meta.LastActive; !got.Equal(boot) {
		t.Fatalf("precondition: expected the cache to still hold the boot copy, got %v", got)
	}

	if n := r.ResyncFromDisk(); n != 1 {
		t.Fatalf("ResyncFromDisk moved %d sessions, want 1", n)
	}
	if got := r.Sessions()["S-turned"].Meta.LastActive; !got.Equal(sess.Meta.LastActive) {
		t.Fatalf("last_active = %v, want the predecessor's %v", got, sess.Meta.LastActive)
	}
	if ids := r.SessionIDs(); len(ids) == 0 || ids[0] != "S-turned" {
		t.Fatalf("SessionIDs order = %v, want S-turned first", ids)
	}
}

// TestResyncNeverRollsBackOwnWrite: a disk copy older than the cache (this
// process wrote after the read) must not replace it.
func TestResyncNeverRollsBackOwnWrite(t *testing.T) {
	layout := newLayout(t)
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "S-own", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	cached := r.Sessions()["S-own"]
	cached.Meta.LastActive = cached.Meta.LastActive.Add(time.Hour)
	cached.Meta.Label = "newer in memory"
	r.upsertSession(cached)

	if n := r.ResyncFromDisk(); n != 0 {
		t.Fatalf("ResyncFromDisk moved %d sessions, want 0", n)
	}
	if got := r.Sessions()["S-own"].Meta.Label; got != "newer in memory" {
		t.Fatalf("label rolled back to %q", got)
	}
}
