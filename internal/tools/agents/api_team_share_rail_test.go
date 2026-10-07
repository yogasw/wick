package agents

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// mwRouter keeps Register's routes and its Use middlewares, so a test can
// run a route's real middleware chain the way wick mounts it.
type mwRouter struct {
	recordingRouter
	routes []string // "METHOD path"
	uses   []struct {
		prefix string
		mw     tool.Middleware
	}
}

func (r *mwRouter) add(m, p string)                     { r.routes = append(r.routes, m+" "+p) }
func (r *mwRouter) GET(p string, _ tool.HandlerFunc)    { r.add("GET", p) }
func (r *mwRouter) POST(p string, _ tool.HandlerFunc)   { r.add("POST", p) }
func (r *mwRouter) PUT(p string, _ tool.HandlerFunc)    { r.add("PUT", p) }
func (r *mwRouter) DELETE(p string, _ tool.HandlerFunc) { r.add("DELETE", p) }
func (r *mwRouter) PATCH(p string, _ tool.HandlerFunc)  { r.add("PATCH", p) }
func (r *mwRouter) Static(string, fs.FS)                {}
func (r *mwRouter) Use(prefix string, mw tool.Middleware) {
	r.uses = append(r.uses, struct {
		prefix string
		mw     tool.Middleware
	}{prefix, mw})
}

// covers is tool.Router.Use's match: the prefix itself or below it, on a
// segment boundary.
func covers(prefix, path string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// chain wraps next in every middleware covering path, first registered
// outermost.
func (r *mwRouter) chain(path string, next tool.HandlerFunc) tool.HandlerFunc {
	h := next
	for i := len(r.uses) - 1; i >= 0; i-- {
		if covers(r.uses[i].prefix, path) {
			h = r.uses[i].mw(h)
		}
	}
	return h
}

// railGuarded reports whether path falls under a shared-chat rail prefix.
func railGuarded(path string) bool {
	for _, p := range sharedChatRailPrefixes {
		if covers(p, path) {
			return true
		}
	}
	return false
}

// A share recipient chats with a shared agent but never reaches its rail:
// every rail route answers 403 on the recipient's chat, the chat routes do
// not, and the owner's own chat keeps its whole rail.
func TestSharedChatRailForbidden(t *testing.T) {
	withTeamWorld(t)
	owner, bob := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	chatOf := func(u *entity.User) string {
		w, c := teamReq(t, u, http.MethodPost, "/", map[string]any{}, map[string]string{"id": p.ID})
		apiTeamAgentChat(c)
		var out struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != http.StatusOK || out.SessionID == "" {
			t.Fatalf("chat as %s: %d %s", u.ID, w.Code, w.Body)
		}
		return out.SessionID
	}
	bobChat, ownChat := chatOf(bob), chatOf(owner)

	rec := &mwRouter{}
	Register(rec)
	// Every rail tab's endpoint must be under the guard.
	for _, want := range []string{
		"GET /sessions/{id}/files", "GET /sessions/{id}/files/read", "GET /sessions/{id}/files/mentions",
		"GET /sessions/{id}/processes", "GET /sessions/{id}/workspace", "GET /sessions/{id}/schedules",
		"POST /sessions/{id}/project", "GET /api/sessions/{id}/subagents", "GET /api/sessions/{id}/team-tasks",
		"GET /api/sessions/{id}/todos", "GET /api/sessions/{id}/git/repos", "GET /api/sessions/{id}/git/status",
		"POST /api/sessions/{id}/git/commit",
	} {
		found := false
		for _, r := range rec.routes {
			found = found || r == want
		}
		if !found || !railGuarded(strings.SplitN(want, " ", 2)[1]) {
			t.Errorf("%s: registered=%v, guarded=%v", want, found, railGuarded(strings.SplitN(want, " ", 2)[1]))
		}
	}
	// The chat itself stays open to the recipient.
	for _, path := range []string{
		"/sessions/{id}/send", "/sessions/{id}/answer", "/sessions/{id}/kill", "/sessions/{id}/uploads/{name}",
		"/api/sessions/{id}/conversation", "/api/sessions/{id}/meta", "/api/sessions/{id}/approvals/{approvalID}",
		"/api/sessions/{id}/postback", "/api/sessions/{id}/context",
	} {
		if railGuarded(path) {
			t.Errorf("chat route %s is rail-guarded", path)
		}
	}

	ran := 0
	sentinel := func(c *tool.Ctx) { ran++; c.JSON(http.StatusOK, map[string]string{}) }
	rail := 0
	for _, route := range rec.routes {
		method, path, _ := strings.Cut(route, " ")
		if !railGuarded(path) {
			continue
		}
		rail++
		h := rec.chain(path, sentinel)
		w, c := teamReq(t, bob, method, "/", nil, map[string]string{"id": bobChat})
		ran = 0
		h(c)
		if w.Code != http.StatusForbidden || ran != 0 {
			t.Errorf("recipient %s: %d (handler ran %d), want 403", route, w.Code, ran)
		}
		w, c = teamReq(t, owner, method, "/", nil, map[string]string{"id": ownChat})
		ran = 0
		h(c)
		if w.Code != http.StatusOK || ran != 1 {
			t.Errorf("owner %s on own chat: %d, want handler to run", route, w.Code)
		}
	}
	if rail < 20 {
		t.Fatalf("only %d rail routes checked", rail)
	}

	// Notes are the owner's project's too.
	w, c := teamReq(t, bob, http.MethodGet, "/api/notes?session_id="+bobChat, nil, nil)
	apiNotesList(c)
	if w.Code == http.StatusOK {
		t.Fatalf("recipient read notes: %d %s", w.Code, w.Body)
	}
}
