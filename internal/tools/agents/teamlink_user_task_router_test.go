package agents

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// routeRecorder is a tool.Router that keeps what Register declares —
// routes and the middlewares of each prefix — so a test runs a route the
// way the server mounts it: every middleware whose prefix covers the
// route's path wraps its handler, the first registered outermost.
type routeRecorder struct {
	routes map[string]tool.HandlerFunc
	mws    []struct {
		prefix string
		mw     tool.Middleware
	}
}

func (r *routeRecorder) Meta() tool.Tool { return tool.Tool{Key: "agents", Path: "/tools/agents"} }
func (r *routeRecorder) add(method, p string, h tool.HandlerFunc) {
	r.routes[method+" "+p] = h
}
func (r *routeRecorder) GET(p string, h tool.HandlerFunc)    { r.add("GET", p, h) }
func (r *routeRecorder) POST(p string, h tool.HandlerFunc)   { r.add("POST", p, h) }
func (r *routeRecorder) PUT(p string, h tool.HandlerFunc)    { r.add("PUT", p, h) }
func (r *routeRecorder) DELETE(p string, h tool.HandlerFunc) { r.add("DELETE", p, h) }
func (r *routeRecorder) PATCH(p string, h tool.HandlerFunc)  { r.add("PATCH", p, h) }
func (r *routeRecorder) Use(prefix string, mw tool.Middleware) {
	r.mws = append(r.mws, struct {
		prefix string
		mw     tool.Middleware
	}{prefix, mw})
}
func (r *routeRecorder) Static(string, fs.FS)                                       {}
func (r *routeRecorder) HandleRaw(string, func(cfg tool.ConfigReader) http.Handler) {}
func (r *routeRecorder) WebhookGroup(string) tool.WebhookRouter                     { return discardHooks{} }

// routed is the handler of method+path as the server runs it.
func (r *routeRecorder) routed(t *testing.T, method, path string) tool.HandlerFunc {
	t.Helper()
	h := r.routes[method+" "+path]
	if h == nil {
		t.Fatalf("no route %s %s", method, path)
	}
	for i := len(r.mws) - 1; i >= 0; i-- {
		if p := r.mws[i].prefix; path == p || strings.HasPrefix(path, p+"/") {
			h = r.mws[i].mw(h)
		}
	}
	return h
}

// P35-7: the answer and cancel routes, run through what Register mounts,
// refuse a read-only viewer of the chat (403) and a stranger (404, the
// chat is not theirs to know), leaving the task untouched; the chat's
// owner gets through.
func TestTeamTaskActionsThroughTheRouter(t *testing.T) {
	testTeamTaskActionsThroughTheRouter(t, "")
}

// P45: the person's own task (an @mention they typed) takes the same
// checks: a read-only viewer or a stranger never acts on it.
func TestUserOriginTaskActionsThroughTheRouter(t *testing.T) {
	testTeamTaskActionsThroughTheRouter(t, teamlink.OriginUser)
}

func testTeamTaskActionsThroughTheRouter(t *testing.T, origin string) {
	withTeamWorld(t)
	owner, bob, eve := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}, &entity.User{ID: "eve"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	ownChat := openChat(t, owner, p.ID, false)
	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	hub := seedUserTasks(t, p.ID, map[string]seedTask{"t-ask": {ownChat, a2a.TaskStateInputRequired, origin}})

	rr := &routeRecorder{routes: map[string]tool.HandlerFunc{}}
	Register(rr)
	call := func(u *entity.User, action string, body any) int {
		w, c := teamReq(t, u, http.MethodPost, "/", body, map[string]string{"id": ownChat, "task": "t-ask"})
		rr.routed(t, http.MethodPost, "/api/sessions/{id}/team-tasks/{task}/"+action)(c)
		return w.Code
	}
	stillAsks := func(when string) {
		t.Helper()
		list := hub.SentFrom(ownChat)
		if len(list) != 1 || list[0].State != "input_required" || list[0].Origin != origin {
			t.Fatalf("%s: task = %+v", when, list)
		}
	}

	for _, action := range []string{"answer", "cancel"} {
		if code := call(bob, action, map[string]any{"text": "prod"}); code != http.StatusForbidden {
			t.Fatalf("read-only viewer %s: %d, want 403", action, code)
		}
		if code := call(eve, action, map[string]any{"text": "prod"}); code != http.StatusNotFound {
			t.Fatalf("stranger %s: %d, want 404", action, code)
		}
		stillAsks("after " + action + " was refused")
	}
	// The owner reaches the handler: cancel settles the question.
	if code := call(owner, "cancel", map[string]any{}); code != http.StatusOK {
		t.Fatalf("owner cancel: %d", code)
	}
	if list := hub.SentFrom(ownChat); len(list) != 1 || list[0].State != "canceled" {
		t.Fatalf("after the owner's cancel: %+v", list)
	}
}
