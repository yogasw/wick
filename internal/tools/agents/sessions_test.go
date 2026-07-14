package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// withSpawnLog points globalSpawnLog at a temp dir and writes the given
// (type, name, session, startedAt, exitReason) spawns as start+exit events.
func withSpawnLog(t *testing.T, specs []spawnSpec) {
	t.Helper()
	sl := provider.NewSpawnLogger(t.TempDir())
	for _, s := range specs {
		p := sl.Path(s.pType, s.pName, s.session, s.at)
		if err := sl.Append(p, provider.SpawnEvent{Type: "start", At: s.at, ProviderType: s.pType, ProviderName: s.pName, SessionID: s.session, PID: s.pid, FirstUserMessage: s.msg}); err != nil {
			t.Fatal(err)
		}
		if s.exit != "" {
			if err := sl.Append(p, provider.SpawnEvent{Type: "exit", At: s.at.Add(time.Second), ExitReason: s.exit}); err != nil {
				t.Fatal(err)
			}
		}
	}
	prev := globalSpawnLog
	globalSpawnLog = sl
	t.Cleanup(func() { globalSpawnLog = prev })
}

type spawnSpec struct {
	pType, pName, session, msg, exit string
	pid                              int
	at                               time.Time
}

func adminCtx(t *testing.T, target string, pathVals map[string]string) (*httptest.ResponseRecorder, *tool.Ctx) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r = r.WithContext(login.WithUser(r.Context(), &entity.User{Role: entity.RoleAdmin}, nil))
	for k, v := range pathVals {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	return w, tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
}

func TestApiSessionsList_GroupsBySession(t *testing.T) {
	base := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	withSpawnLog(t, []spawnSpec{
		// session A: 3 spawns, newest is "stopped"
		{pType: "claude", pName: "claude", session: "aaa", msg: "first A", exit: "idle", at: base},
		{pType: "claude", pName: "claude", session: "aaa", msg: "second A", exit: "error", at: base.Add(1 * time.Minute)},
		{pType: "claude", pName: "claude", session: "aaa", msg: "third A", exit: "stopped", at: base.Add(2 * time.Minute)},
		// session B: 1 spawn
		{pType: "codex", pName: "gemini", session: "bbb", msg: "only B", exit: "clean", at: base.Add(3 * time.Minute)},
	})

	w, c := adminCtx(t, "/api/providers/sessions", nil)
	apiSessionsList(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp SessionsListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 2 {
		t.Fatalf("want 2 sessions, got %d", resp.Total)
	}
	// Newest session (B) first.
	if resp.Sessions[0].SessionID != "bbb" {
		t.Errorf("first session = %q, want bbb", resp.Sessions[0].SessionID)
	}
	var a *SessionSummaryDTO
	for i := range resp.Sessions {
		if resp.Sessions[i].SessionID == "aaa" {
			a = &resp.Sessions[i]
		}
	}
	if a == nil {
		t.Fatal("session aaa missing")
	}
	if a.SpawnCount != 3 {
		t.Errorf("aaa spawn count = %d, want 3", a.SpawnCount)
	}
	if a.LastStatus != "stopped" {
		t.Errorf("aaa last status = %q, want stopped (newest spawn)", a.LastStatus)
	}
	if a.FirstMessage != "third A" {
		t.Errorf("aaa first message = %q, want the newest spawn's message", a.FirstMessage)
	}
}

func TestApiSessionsList_SearchMatchesAnySpawn(t *testing.T) {
	base := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	withSpawnLog(t, []spawnSpec{
		{pType: "claude", pName: "claude", session: "aaa", msg: "needle here", exit: "idle", at: base},
		{pType: "claude", pName: "claude", session: "aaa", msg: "newest boring", exit: "stopped", at: base.Add(time.Minute)},
	})
	// The newest spawn's message is "newest boring"; searching an older
	// spawn's message must still surface the session.
	w, c := adminCtx(t, "/api/providers/sessions?q=needle", nil)
	apiSessionsList(c)
	var resp SessionsListResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 1 {
		t.Fatalf("q=needle should match session via an older spawn; got %d", resp.Total)
	}
}

func TestApiSessionSpawns_ReturnsAllForSession(t *testing.T) {
	base := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	withSpawnLog(t, []spawnSpec{
		{pType: "claude", pName: "claude", session: "aaa", msg: "one", exit: "idle", at: base},
		{pType: "claude", pName: "claude", session: "aaa", msg: "two", exit: "stopped", at: base.Add(time.Minute)},
		{pType: "codex", pName: "gemini", session: "bbb", msg: "other", exit: "clean", at: base},
	})
	w, c := adminCtx(t, "/api/providers/sessions/aaa", map[string]string{"id": "aaa"})
	apiSessionSpawns(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var resp SessionSpawnsResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Spawns) != 2 {
		t.Fatalf("session aaa should have 2 spawns, got %d", len(resp.Spawns))
	}
	if resp.ProviderType != "claude" {
		t.Errorf("provider type = %q, want claude", resp.ProviderType)
	}
}
