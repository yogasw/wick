package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

/* The install this has to survive is not one big project: it is thousands of
   sessions spread over ~20 projects, where any one person can reach only a
   handful of them. Every list request still walks the whole session set to
   decide what that person may see, so the cost that matters is
   "sessions total", not "sessions in my projects". These benchmarks pin that
   down instead of guessing. */

const (
	scaleSessions = 5000
	scaleProjects = 20
	scaleMine     = 3 // projects the caller can actually reach
)

// withManyProjects seeds scaleSessions sessions round-robin across
// scaleProjects projects. The first scaleMine projects are OWNED by
// ownerID, which is what makes them reachable for a non-admin caller
// (callerProjectAccess unions tag grants with project ownership); the rest
// belong to someone else and must be filtered out on every request.
//
// Sessions are written as raw meta.json for the same reason as
// withSessionWorld: session.Create fsyncs, and 5000 fsyncs dominate the
// benchmark instead of the code under test.
func withManyProjects(t testing.TB, ownerID string) agentconfig.Layout {
	t.Helper()
	layout := agentconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	for p := 0; p < scaleProjects; p++ {
		pid := fmt.Sprintf("p%02d", p)
		owner := "someone-else"
		if p < scaleMine {
			owner = ownerID
		}
		if _, err := project.Create(layout, project.CreateOptions{ID: pid, Name: pid, OwnerUserID: owner}); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < scaleSessions; i++ {
		id := fmt.Sprintf("s-%05d", i)
		pid := fmt.Sprintf("p%02d", i%scaleProjects)
		meta := session.Meta{
			ProjectID:  pid,
			Origin:     session.OriginSlack,
			Status:     session.StatusIdle,
			CreatedAt:  base,
			LastActive: base.Add(time.Duration(i) * time.Second),
			Label:      fmt.Sprintf("session %d label", i),
		}
		// Every 3rd session is a shared Slack thread the caller took part
		// in — the multi-participant shape, so the filter does the real
		// slice walk rather than the one-comparison fast path.
		switch i % 3 {
		case 0:
			meta.UserID = ownerID
			meta.Participants = []string{ownerID}
		case 1:
			meta.UserID = "starter-" + fmt.Sprint(i%7)
			meta.Participants = []string{meta.UserID, "other-a", ownerID}
		default:
			meta.UserID = "starter-" + fmt.Sprint(i%7)
			meta.Participants = []string{meta.UserID, "other-a", "other-b"}
		}
		if err := os.MkdirAll(layout.SessionDir(id), 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(layout.SessionMeta(id), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := registry.New(layout)
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}
	prevMgr, prevLayout, prevPool := globalMgr, globalLayout, globalPool
	globalMgr, globalLayout = registry.NewManager(reg), layout
	globalPool = pool.New(pool.PoolConfig{Layout: layout})
	t.Cleanup(func() { globalMgr, globalLayout, globalPool = prevMgr, prevLayout, prevPool })
	return layout
}

// BenchmarkSessionListManyProjects: 5000 sessions / 20 projects, caller can
// reach 3 of them. Answers "is the list still fast when the install grows",
// per scope the UI actually requests.
func BenchmarkSessionListManyProjects(b *testing.B) {
	t := &testing.T{}
	withManyProjects(t, "bob")
	bob := &entity.User{ID: "bob", Role: entity.RoleUser}

	run := func(b *testing.B, target string) {
		b.Helper()
		fetchSessionList(t, bob, target) // warm
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			w, c := userCtx(t, bob, target, nil)
			apiSessionList(c)
			if w.Code != 200 {
				b.Fatalf("status=%d", w.Code)
			}
		}
	}

	b.Run("yours-across-all-projects", func(b *testing.B) { run(b, "/api/sessions?owner=me") })
	b.Run("all-i-can-see", func(b *testing.B) { run(b, "/api/sessions") })
	b.Run("scoped-project-yours", func(b *testing.B) { run(b, "/api/sessions?project=p01&owner=me") })
	b.Run("scoped-project-deep-offset", func(b *testing.B) { run(b, "/api/sessions?project=p01&offset=200") })

	// The warm cases above never write. A live install does: every turn
	// flips a session's status, and each write invalidates the registry's
	// cached snapshot + sorted id list, so the NEXT request rebuilds both
	// (copy the whole session map, then sort every id). That rebuild — not
	// the visibility filter — is what actually grows with session count,
	// so measure a request that has to pay for it.
	b.Run("yours-after-a-meta-write", func(b *testing.B) {
		target := "/api/sessions?owner=me"
		fetchSessionList(t, bob, target)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := globalMgr.RefreshSession(fmt.Sprintf("s-%05d", i%scaleSessions)); err != nil {
				b.Fatal(err)
			}
			w, c := userCtx(t, bob, target, nil)
			apiSessionList(c)
			if w.Code != 200 {
				b.Fatalf("status=%d", w.Code)
			}
		}
	})
}

// TestSessionListManyProjects_ShapeAndFootprint is not a pass/fail perf gate
// (a shared CI box cannot hold one) — it reports the numbers a capacity
// question needs: what a caller with 3 of 20 projects actually receives, and
// what 5000 session metas cost in memory. Run with -v to read them.
func TestSessionListManyProjects_ShapeAndFootprint(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 5000 sessions on disk")
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	withManyProjects(t, "bob")
	runtime.GC()
	runtime.ReadMemStats(&after)

	bob := &entity.User{ID: "bob", Role: entity.RoleUser}

	all := fetchSessionList(t, bob, "/api/sessions")
	mine := fetchSessionList(t, bob, "/api/sessions?owner=me")
	scoped := fetchSessionList(t, bob, "/api/sessions?project=p01&owner=me")

	// 3 of 20 projects reachable -> 15% of the sessions are visible at all.
	wantVisible := scaleSessions * scaleMine / scaleProjects
	if all.Total != wantVisible {
		t.Fatalf("visible total = %d, want %d (only owned projects)", all.Total, wantVisible)
	}
	if len(all.Sessions) > sessionListCap {
		t.Fatalf("page carried %d rows, want <= %d", len(all.Sessions), sessionListCap)
	}
	if mine.Total >= all.Total {
		t.Fatalf("yours (%d) must be a subset of visible (%d)", mine.Total, all.Total)
	}
	if scoped.Total == 0 || scoped.Total > mine.Total {
		t.Fatalf("scoped yours = %d, expected between 1 and %d", scoped.Total, mine.Total)
	}

	t.Logf("sessions=%d projects=%d reachable=%d | visible=%d yours=%d scoped-yours=%d rows/page=%d",
		scaleSessions, scaleProjects, scaleMine, all.Total, mine.Total, scoped.Total, len(all.Sessions))
	t.Logf("registry heap for %d session metas: %.1f MB (%.0f B/session)",
		scaleSessions,
		float64(after.HeapAlloc-before.HeapAlloc)/(1024*1024),
		float64(after.HeapAlloc-before.HeapAlloc)/float64(scaleSessions))
}
