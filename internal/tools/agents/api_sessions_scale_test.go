package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

/* These tests exist because the sessions list grew tabs and paging for
   projects holding hundreds of chats: the owner scope must count and filter
   server-side, and "Load more" must walk the whole set without dropping or
   repeating a row. They run against a real layout + registry seeded with
   over a thousand sessions — the shape of the project that motivated the
   change — not a handful. */

// seededSession is one session to materialize: who owns it and where.
type seededSession struct {
	id     string
	userID string
}

// withSessionWorld builds a real temp layout holding project "p1" plus the
// given sessions (each with a distinct LastActive so list order is total),
// wires the package globals apiSessionList reads, and restores them after.
//
// Sessions are seeded as raw meta.json files rather than through
// session.Create: the atomic-write path fsyncs each file, which at a
// thousand sessions costs minutes on Windows and times the suite out. The
// registry only reads the files, so the shortcut changes nothing observed.
func withSessionWorld(t *testing.T, seeds []seededSession) {
	t.Helper()
	layout := agentconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Create(layout, project.CreateOptions{ID: "p1", Name: "one"}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i, s := range seeds {
		meta := session.Meta{
			ProjectID: "p1",
			Origin:    session.OriginUI,
			Status:    session.StatusIdle,
			CreatedAt: base,
			// Distinct LastActive per session: the list sorts by it, and
			// offset paging is only well-defined over a total order.
			LastActive: base.Add(time.Duration(i) * time.Second),
			UserID:     s.userID,
		}
		if err := os.MkdirAll(layout.SessionDir(s.id), 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(layout.SessionMeta(s.id), b, 0o644); err != nil {
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
}

// userCtx builds a request context for the given user (admin by default so
// the ownerless test project is visible; owner=me still narrows to u.ID).
func userCtx(t *testing.T, u *entity.User, target string, pathVals map[string]string) (*httptest.ResponseRecorder, *tool.Ctx) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r = r.WithContext(login.WithUser(r.Context(), u, nil))
	for k, v := range pathVals {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	return w, tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
}

type sessionListResp struct {
	Sessions []SessionListItem `json:"sessions"`
	Total    int               `json:"total"`
	HasMore  bool              `json:"has_more"`
}

func fetchSessionList(t *testing.T, u *entity.User, target string) sessionListResp {
	t.Helper()
	w, c := userCtx(t, u, target, nil)
	apiSessionList(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp sessionListResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// A project of 1200 chats: the default scope loads only the caller's, the
// full scope pages, and every count is the real one — not the page size.
func TestApiSessionList_OwnerScopeAndPagingAtScale(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 1200 sessions on disk")
	}
	const mine, others = 30, 1170
	seeds := make([]seededSession, 0, mine+others)
	for i := 0; i < mine; i++ {
		seeds = append(seeds, seededSession{id: fmt.Sprintf("mine-%04d", i), userID: "admin"})
	}
	for i := 0; i < others; i++ {
		seeds = append(seeds, seededSession{id: fmt.Sprintf("bob-%04d", i), userID: "bob"})
	}
	withSessionWorld(t, seeds)
	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin}

	// Default tab: only the caller's sessions, counted exactly.
	got := fetchSessionList(t, admin, "/api/sessions?project=p1&owner=me")
	if got.Total != mine || len(got.Sessions) != mine || got.HasMore {
		t.Fatalf("owner=me: total=%d rows=%d has_more=%v, want %d/%d/false",
			got.Total, len(got.Sessions), got.HasMore, mine, mine)
	}
	for _, s := range got.Sessions {
		if s.ID[:5] != "mine-" {
			t.Fatalf("owner=me leaked a foreign session: %s", s.ID)
		}
	}

	// "All" tab: real total, one page of rows, more offered.
	got = fetchSessionList(t, admin, "/api/sessions?project=p1")
	if got.Total != mine+others {
		t.Fatalf("all: total=%d, want %d", got.Total, mine+others)
	}
	if len(got.Sessions) != sessionListCap || !got.HasMore {
		t.Fatalf("all: rows=%d has_more=%v, want %d/true", len(got.Sessions), got.HasMore, sessionListCap)
	}

	// "Load more" walks the whole set: no duplicates, no gaps, and the
	// last page says so.
	seen := map[string]bool{}
	for offset := 0; ; {
		page := fetchSessionList(t, admin, fmt.Sprintf("/api/sessions?project=p1&offset=%d", offset))
		for _, s := range page.Sessions {
			if seen[s.ID] {
				t.Fatalf("offset paging repeated %s", s.ID)
			}
			seen[s.ID] = true
		}
		offset += len(page.Sessions)
		if !page.HasMore {
			break
		}
		if len(page.Sessions) == 0 {
			t.Fatal("has_more with an empty page would loop forever")
		}
	}
	if len(seen) != mine+others {
		t.Fatalf("paging saw %d sessions, want %d", len(seen), mine+others)
	}

	// An offset past the end is an empty page, not an error.
	got = fetchSessionList(t, admin, "/api/sessions?project=p1&offset=99999")
	if len(got.Sessions) != 0 || got.HasMore {
		t.Fatalf("past-the-end offset: rows=%d has_more=%v, want 0/false", len(got.Sessions), got.HasMore)
	}
}

// How fast one list request is against a 1200-session project — the number
// behind "opening a project loads only your page". Run with:
//
//	go test ./internal/tools/agents/ -bench BenchmarkApiSessionList -run xxx
func BenchmarkApiSessionList(b *testing.B) {
	const mine, others = 30, 1170
	seeds := make([]seededSession, 0, mine+others)
	for i := 0; i < mine; i++ {
		seeds = append(seeds, seededSession{id: fmt.Sprintf("mine-%04d", i), userID: "admin"})
	}
	for i := 0; i < others; i++ {
		seeds = append(seeds, seededSession{id: fmt.Sprintf("bob-%04d", i), userID: "bob"})
	}
	t := &testing.T{}
	withSessionWorld(t, seeds)
	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin}

	run := func(b *testing.B, target string) {
		b.Helper()
		// Warm once so label reads hit the cache — the steady state a user
		// actually browses in.
		fetchSessionList(t, admin, target)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			w, c := userCtx(t, admin, target, nil)
			apiSessionList(c)
			if w.Code != http.StatusOK {
				b.Fatalf("status=%d", w.Code)
			}
		}
	}

	b.Run("owner=me", func(b *testing.B) { run(b, "/api/sessions?project=p1&owner=me") })
	b.Run("all-first-page", func(b *testing.B) { run(b, "/api/sessions?project=p1") })
	b.Run("all-deep-offset", func(b *testing.B) { run(b, "/api/sessions?project=p1&offset=1150") })
}

// Under ticket mode, "your sessions" includes a chat someone else started on
// a ticket assigned to you — it is your work — and stops including it when
// ticket mode is off.
func TestApiSessionList_TicketAssignedCountsAsMine(t *testing.T) {
	withSessionWorld(t, []seededSession{
		{id: "sa", userID: "admin"},
		{id: "sb1", userID: "bob"},
		{id: "sb2", userID: "bob"},
	})
	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin}

	// Ticket mode off: ownership is the whole story.
	got := fetchSessionList(t, admin, "/api/sessions?project=p1&owner=me")
	if got.Total != 1 || got.Sessions[0].ID != "sa" {
		t.Fatalf("ticket mode off: total=%d, want just sa", got.Total)
	}

	// Enable ticket mode and assign bob's chat to admin via a ticket.
	p, ok := globalMgr.Registry().Project("p1")
	if !ok {
		t.Fatal("project p1 missing")
	}
	meta := p.Meta
	meta.Ticket.Enabled = true
	if _, err := globalMgr.UpdateProject(context.Background(), "p1", meta); err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.Create(globalLayout, ticket.CreateOptions{
		ProjectID: "p1", Title: "assigned work", Assignee: "admin", Sessions: []string{"sb1"},
	}); err != nil {
		t.Fatal(err)
	}

	got = fetchSessionList(t, admin, "/api/sessions?project=p1&owner=me")
	if got.Total != 2 {
		t.Fatalf("ticket mode on: total=%d, want 2 (own + assigned)", got.Total)
	}
	ids := map[string]bool{}
	for _, s := range got.Sessions {
		ids[s.ID] = true
	}
	if !ids["sa"] || !ids["sb1"] {
		t.Fatalf("owner=me should hold sa + sb1, got %v", ids)
	}
	if ids["sb2"] {
		t.Fatal("sb2 is bob's and unassigned — it must not count as mine")
	}
}

// The untracked rail's owner scope filters BOTH the rows and the count, so
// the tab's number always describes the set it shows.
func TestApiProjectTickets_UntrackedOwnerScope(t *testing.T) {
	withSessionWorld(t, []seededSession{
		{id: "ua1", userID: "admin"},
		{id: "ua2", userID: "admin"},
		{id: "ub1", userID: "bob"},
		{id: "ub2", userID: "bob"},
		{id: "ub3", userID: "bob"},
	})
	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin}

	fetchBoard := func(target string) ticketBoardResponse {
		t.Helper()
		w, c := userCtx(t, admin, target, map[string]string{"id": "p1"})
		apiProjectTickets(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var resp ticketBoardResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Mine: two rows, counted as two — bob's three never travel.
	resp := fetchBoard("/api/projects/p1/tickets?untracked=1&untracked_owner=me")
	if resp.UntrackedTotal != 2 || len(resp.Untracked) != 2 {
		t.Fatalf("untracked_owner=me: total=%d rows=%d, want 2/2", resp.UntrackedTotal, len(resp.Untracked))
	}
	for _, row := range resp.Untracked {
		if row.ID[:2] != "ua" {
			t.Fatalf("mine scope leaked %s", row.ID)
		}
	}

	// Absent param: the historical everyone view.
	resp = fetchBoard("/api/projects/p1/tickets?untracked=1")
	if resp.UntrackedTotal != 5 || len(resp.Untracked) != 5 {
		t.Fatalf("all scope: total=%d rows=%d, want 5/5", resp.UntrackedTotal, len(resp.Untracked))
	}
}
