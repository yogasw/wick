package managedbin

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The request path (Status, Releases, LatestSnapshot) must never reach
// GitHub — only RefreshLatest does.
func TestStatusServesSnapshotWithoutNetwork(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.scripts["v1.2.3"] = script(filepath.Join(t.TempDir(), "a"), "fakecli 1.2.3")
	gh.latestTag = "v1.2.3"
	m, _ := newTestManager(t, gh)

	st, err := m.Status("fake")
	if err != nil || st.Latest != nil || gh.api.Load() != 0 {
		t.Fatalf("cold status: %v %+v api=%d", err, st.Latest, gh.api.Load())
	}
	if _, err := m.RefreshLatest(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	calls := gh.api.Load()
	for i := 0; i < 5; i++ {
		st, _ = m.Status("fake")
		_, _ = m.Releases("fake")
	}
	if gh.api.Load() != calls {
		t.Fatalf("status/releases hit GitHub: %d → %d", calls, gh.api.Load())
	}
	if st.Latest == nil || st.Latest.Tag != "v1.2.3" || len(st.Releases) != 1 || st.LatestCheckedAt.IsZero() {
		t.Fatalf("status snapshot: %+v", st)
	}
	if len(st.Releases[0].Assets) != 0 || len(st.Latest.Assets) != 0 {
		t.Fatal("assets must be stripped from the snapshot")
	}
}

func TestRefreshLatestCoalesces(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.scripts["v1.2.3"] = script(filepath.Join(t.TempDir(), "a"), "fakecli 1.2.3")
	gh.latestTag = "v1.2.3"
	gh.block = make(chan struct{})
	m, _ := newTestManager(t, gh)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, err := m.RefreshLatest(context.Background(), "fake"); err != nil || s.Latest == nil {
				t.Errorf("refresh: %v %+v", err, s)
			}
		}()
	}
	// Let every caller arrive, then release the one real request.
	deadline := time.Now().Add(2 * time.Second)
	for gh.api.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(gh.block)
	wg.Wait()
	// One refresh = one /latest + one /releases.
	if n := gh.api.Load(); n != 2 {
		t.Fatalf("coalesced refresh made %d API calls, want 2", n)
	}
}

func TestLatestCacheFileWarmsBoot(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.scripts["v1.2.3"] = script(filepath.Join(t.TempDir(), "a"), "fakecli 1.2.3")
	gh.latestTag = "v1.2.3"
	m, root := newTestManager(t, gh)
	if _, err := m.RefreshLatest(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	calls := gh.api.Load()

	// "Restart": a fresh manager on the same dir.
	m2, _ := newTestManager(t, gh)
	m2.Root = func() string { return root }
	st, _ := m2.Status("fake")
	if st.Latest == nil || st.Latest.Tag != "v1.2.3" {
		t.Fatalf("boot did not read latest-cache.json: %+v", st)
	}
	m2.Enabled = func(typ string) bool { return typ == "fake" }
	m2.refreshStale(context.Background())
	if gh.api.Load() != calls {
		t.Fatalf("fresh file cache still refreshed: %d → %d", calls, gh.api.Load())
	}
	// Once older than the TTL, the loop does refresh.
	m2.mu.Lock()
	s := m2.snaps["fake"]
	s.FetchedAt = time.Now().Add(-LatestTTL - time.Minute)
	m2.snaps["fake"] = s
	m2.mu.Unlock()
	m2.refreshStale(context.Background())
	if gh.api.Load() != calls+2 {
		t.Fatalf("stale snapshot not refreshed: %d → %d", calls, gh.api.Load())
	}
	// Disabled types are never checked.
	m2.Enabled = func(string) bool { return false }
	m2.mu.Lock()
	m2.snaps = map[string]LatestSnapshot{}
	m2.mu.Unlock()
	m2.refreshStale(context.Background())
	if gh.api.Load() != calls+2 {
		t.Fatal("disabled type was checked")
	}
}

func TestForceRefreshRateLimited(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.scripts["v1.2.3"] = script(filepath.Join(t.TempDir(), "a"), "fakecli 1.2.3")
	gh.latestTag = "v1.2.3"
	m, _ := newTestManager(t, gh)
	ctx := context.Background()
	if _, _, err := m.ForceRefreshLatest(ctx, "fake"); err != nil {
		t.Fatal(err)
	}
	calls := gh.api.Load()
	s, wait, err := m.ForceRefreshLatest(ctx, "fake")
	if !errors.Is(err, ErrRefreshTooSoon) || wait <= 0 || wait > ForceRefreshMinGap || s.Latest == nil {
		t.Fatalf("second force: %v wait=%s", err, wait)
	}
	if gh.api.Load() != calls {
		t.Fatal("rate-limited force still hit GitHub")
	}
	m.mu.Lock()
	m.forcedAt["fake"] = time.Now().Add(-ForceRefreshMinGap - time.Second)
	m.mu.Unlock()
	if _, _, err := m.ForceRefreshLatest(ctx, "fake"); err != nil || gh.api.Load() != calls+2 {
		t.Fatalf("force after the gap: %v api=%d", err, gh.api.Load())
	}
}

func TestRefreshFailureKeepsLastGoodAnswer(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.scripts["v1.2.3"] = script(filepath.Join(t.TempDir(), "a"), "fakecli 1.2.3")
	gh.latestTag = "v1.2.3"
	m, _ := newTestManager(t, gh)
	if _, err := m.RefreshLatest(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	gh.latestTag = "" // /latest now 404s
	if _, err := m.RefreshLatest(context.Background(), "fake"); err == nil {
		t.Fatal("want an error")
	}
	st, _ := m.Status("fake")
	if st.Latest == nil || st.Latest.Tag != "v1.2.3" || st.LatestErr == "" {
		t.Fatalf("last good answer lost: %+v err=%q", st.Latest, st.LatestErr)
	}
}

// Download-only stores + verifies but leaves current alone — except on a
// first install. Activate then switches instantly without downloading.
func TestDownloadOnlyVsActivate(t *testing.T) {
	gh := newFakeGitHub(t)
	dir := t.TempDir()
	gh.scripts["v1.2.3"] = script(filepath.Join(dir, "a"), "fakecli 1.2.3")
	gh.scripts["v1.2.4"] = script(filepath.Join(dir, "b"), "fakecli 1.2.4")
	m, _ := newTestManager(t, gh)
	ctx := context.Background()

	j, err := m.Download(ctx, "fake", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if _, v, _ := m.CurrentPath("fake"); v != "1.2.3" {
		t.Fatalf("first download must activate, current=%q (%+v)", v, j)
	}
	j, err = m.Download(ctx, "fake", "v1.2.4")
	if err != nil || j.Activate || j.Phase != PhaseDone || !strings.HasPrefix(j.Message, "downloaded ") {
		t.Fatalf("download: %v %+v", err, j)
	}
	if _, v, _ := m.CurrentPath("fake"); v != "1.2.3" {
		t.Fatalf("download-only moved current to %s", v)
	}
	st, _ := m.Status("fake")
	var got *InstalledView
	for i := range st.Installed {
		if st.Installed[i].Version == "1.2.4" {
			got = &st.Installed[i]
		}
	}
	if got == nil || got.Current || !got.Removable || got.SHA256 == "" {
		t.Fatalf("1.2.4 not stored as a downloaded, non-current version: %+v", got)
	}
	// Downloading it again is a no-op that still does not activate.
	if j, err := m.Download(ctx, "fake", "v1.2.4"); err != nil || j.Message != "already downloaded" || gh.downloads.Load() != 2 {
		t.Fatalf("re-download: %v %+v dl=%d", err, j, gh.downloads.Load())
	}
	if err := m.Activate("fake", "1.2.4"); err != nil {
		t.Fatal(err)
	}
	if _, v, _ := m.CurrentPath("fake"); v != "1.2.4" || gh.downloads.Load() != 2 {
		t.Fatalf("activate: current=%s dl=%d", v, gh.downloads.Load())
	}
}

// A second start while a job runs returns the running job (so the UI can
// follow it) together with ErrJobRunning.
func TestStartInstallReturnsRunningJob(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.scripts["v1.2.3"] = script(filepath.Join(t.TempDir(), "a"), "fakecli 1.2.3")
	m, _ := newTestManager(t, gh)
	running := &JobInfo{ID: "fake-1", Type: "fake", Tag: "v1.2.3", Phase: PhaseDownload, Done: 5, Total: 10}
	m.mu.Lock()
	m.jobs["fake"] = running
	m.mu.Unlock()
	j, err := m.StartInstall("fake", "v1.2.4", false)
	if !errors.Is(err, ErrJobRunning) || j == nil || j.ID != "fake-1" || j.Done != 5 {
		t.Fatalf("want the running job + ErrJobRunning, got %+v %v", j, err)
	}
}

// The returned job is a snapshot taken under m.mu; the install goroutine
// mutates the live one concurrently (go test -race).
func TestStartInstallSnapshotDoesNotRaceTheJob(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.scripts["v1.2.3"] = script(filepath.Join(t.TempDir(), "a"), "fakecli 1.2.3")
	m, _ := newTestManager(t, gh)
	j, err := m.StartInstall("fake", "v1.2.3", true)
	if err != nil || j == nil || j.Phase != PhaseResolve {
		t.Fatalf("start: %+v %v", j, err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		m.mu.Lock()
		running := m.jobs["fake"].Running()
		m.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
