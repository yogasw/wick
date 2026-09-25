package agentmemory

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/yogasw/wick/pkg/tool"
)

// The handler tests drive the endpoint functions directly with a stubbed
// backend and config store. Nothing here spawns a daemon or shells out: the
// DataSource is a fake, so what is under test is wick's own translation —
// which flags a request turns into, which failures become a named reason, and
// which ones are refused outright.

// fakeStore is an in-memory ConfigStore. The two access flags are separate so
// a VIEWER — logged in, may look, may not manage — is expressible, which is
// the whole point of the split (PLAN §23).
type fakeStore struct {
	enabled bool
	// admin drives ManageAllowed, and reads too: someone who may drive the
	// daemon can obviously look at it, which is also true in production
	// where an admin is a logged-in user.
	admin bool
	// viewer drives ReadAllowed alone — a non-admin who is logged in.
	viewer  bool
	set     Settings
	saved   *Settings
	saveErr error
}

func (f *fakeStore) Enabled() bool                      { return f.enabled }
func (f *fakeStore) ReadAllowed(context.Context) bool   { return f.viewer || f.admin }
func (f *fakeStore) ManageAllowed(context.Context) bool { return f.admin }
func (f *fakeStore) Settings(string) Settings           { return f.set }
func (f *fakeStore) SaveSettings(_ context.Context, _ string, s Settings) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = &s
	f.set = s
	return nil
}

// fakeData is a DataSource whose every method is a hook, so a test can decide
// per call what the backend "answered".
type fakeData struct {
	status    func() (*StoreStatus, error)
	projects  func() ([]ProjectRow, error)
	health    func(ReadScope) (*HealthReport, error)
	handoffs  func(ReadScope, int) ([]Handoff, error)
	search    func(ReadScope, string) ([]SearchHit, error)
	backfill  func(BackfillRequest) (*BackfillReport, error)
	lastReq   BackfillRequest
	backfills int
}

func (f *fakeData) StoreStatus(context.Context, Conn) (*StoreStatus, error) {
	if f.status != nil {
		return f.status()
	}
	return &StoreStatus{Version: "2.4.0"}, nil
}

func (f *fakeData) Projects(context.Context, Conn) ([]ProjectRow, error) {
	if f.projects != nil {
		return f.projects()
	}
	return nil, nil
}

func (f *fakeData) Health(_ context.Context, _ Conn, s ReadScope) (*HealthReport, error) {
	if f.health != nil {
		return f.health(s)
	}
	return &HealthReport{}, nil
}

func (f *fakeData) Handoffs(_ context.Context, _ Conn, s ReadScope, limit int) ([]Handoff, error) {
	if f.handoffs != nil {
		return f.handoffs(s, limit)
	}
	return nil, nil
}

func (f *fakeData) Search(_ context.Context, _ Conn, s ReadScope, q string) ([]SearchHit, error) {
	if f.search != nil {
		return f.search(s, q)
	}
	return nil, nil
}

func (f *fakeData) Backfill(_ context.Context, _ Conn, req BackfillRequest) (*BackfillReport, error) {
	f.lastReq, f.backfills = req, f.backfills+1
	if f.backfill != nil {
		return f.backfill(req)
	}
	return &BackfillReport{DryRun: req.DryRun}, nil
}

// testBackend builds a backend that is registered nowhere — the handlers take
// it as an argument, so the process-wide registry stays untouched.
func testBackend(data DataSource) *Backend {
	d := Descriptor{ID: "test-mem", DisplayName: "test-mem", BinName: "test-mem", PrefPort: 49999, HealthPath: "/healthz", Data: data}
	return &Backend{Desc: d, Mgr: newManager(d)}
}

// withStore installs a config store for one test and restores the previous one.
func withStore(t *testing.T, s ConfigStore) {
	t.Helper()
	prev := store
	store = s
	t.Cleanup(func() { store = prev })
}

// post builds a Ctx over a form-encoded POST.
func post(form url.Values) (*httptest.ResponseRecorder, *tool.Ctx) {
	r := httptest.NewRequest(http.MethodPost, "/tools/agents/agentmemory/test-mem", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	return w, tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
}

// get builds a Ctx over a GET with a query string.
func get(q url.Values) (*httptest.ResponseRecorder, *tool.Ctx) {
	r := httptest.NewRequest(http.MethodGet, "/tools/agents/agentmemory/test-mem?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	return w, tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return m
}

// TestGateFailsClosed: the master switch answers 404 so a disabled feature
// looks absent rather than forbidden, and someone with no access gets 403. An
// unwired store denies too — no request should ever slip through before boot.
//
// Both wrappers are driven, because the 404 half is the half that must NOT
// have changed when the gate was split: a host with the feature off still
// looks like a host without the feature, whoever is asking (PLAN §23.2).
func TestGateFailsClosed(t *testing.T) {
	cases := []struct {
		name  string
		store ConfigStore
		want  int
	}{
		{"unwired store", nil, http.StatusNotFound},
		{"master off, admin", &fakeStore{enabled: false, admin: true}, http.StatusNotFound},
		{"master off, viewer", &fakeStore{enabled: false, viewer: true}, http.StatusNotFound},
		{"no access at all", &fakeStore{enabled: true}, http.StatusForbidden},
	}
	for _, tc := range cases {
		for _, w := range []struct {
			kind string
			fn   func(*Backend, func(*Backend, *tool.Ctx)) tool.HandlerFunc
		}{{"view", view}, {"manage", manage}} {
			t.Run(tc.name+"/"+w.kind, func(t *testing.T) {
				withStore(t, tc.store)
				if tc.store == nil {
					store = nil
				}
				rec, c := get(nil)
				called := false
				w.fn(testBackend(&fakeData{}), func(*Backend, *tool.Ctx) { called = true })(c)
				if called {
					t.Fatal("handler ran behind a closed gate")
				}
				if rec.Code != tc.want {
					t.Fatalf("status %d, want %d", rec.Code, tc.want)
				}
			})
		}
	}
}

// TestViewerGate is the security gate of PLAN §23 stated as a table: admin
// MANAGES, everyone logged in LOOKS.
//
// It drives the two wrappers rather than the handlers, because what is being
// asserted is which side of the split each endpoint was registered on — and
// that is decided by the wrapper. The endpoint-to-wrapper map is asserted
// separately in TestEndpointsAreOnTheRightSide, from the routes themselves.
func TestViewerGate(t *testing.T) {
	cases := []struct {
		name   string
		store  *fakeStore
		view   bool
		manage bool
	}{
		{"admin does both", &fakeStore{enabled: true, admin: true}, true, true},
		{"viewer looks, does not manage", &fakeStore{enabled: true, viewer: true}, true, false},
		{"nobody does neither", &fakeStore{enabled: true}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withStore(t, tc.store)
			for _, w := range []struct {
				kind string
				fn   func(*Backend, func(*Backend, *tool.Ctx)) tool.HandlerFunc
				want bool
			}{{"view", view, tc.view}, {"manage", manage, tc.manage}} {
				rec, c := get(nil)
				called := false
				w.fn(testBackend(&fakeData{}), func(*Backend, *tool.Ctx) { called = true })(c)
				if called != w.want {
					t.Fatalf("%s: ran=%v, want %v", w.kind, called, w.want)
				}
				if !w.want && rec.Code != http.StatusForbidden {
					t.Fatalf("%s: status %d, want 403", w.kind, rec.Code)
				}
			}
		})
	}
}

// recordingRouter captures what RegisterRoutes wires, so the table below is
// the REGISTERED routes rather than a second list that can drift from them. A
// route added later shows up here on its own and has to be classified.
type recordingRouter struct{ routes map[string]tool.HandlerFunc }

func (r *recordingRouter) GET(path string, h tool.HandlerFunc)                    { r.routes["GET "+path] = h }
func (r *recordingRouter) POST(path string, h tool.HandlerFunc)                   { r.routes["POST "+path] = h }
func (r *recordingRouter) PUT(path string, h tool.HandlerFunc)                    { r.routes["PUT "+path] = h }
func (r *recordingRouter) DELETE(path string, h tool.HandlerFunc)                 { r.routes["DELETE "+path] = h }
func (r *recordingRouter) PATCH(path string, h tool.HandlerFunc)                  { r.routes["PATCH "+path] = h }
func (r *recordingRouter) Use(string, tool.Middleware)                            {}
func (r *recordingRouter) Static(string, fs.FS)                                   {}
func (r *recordingRouter) HandleRaw(string, func(tool.ConfigReader) http.Handler) {}
func (r *recordingRouter) WebhookGroup(string) tool.WebhookRouter                 { return nil }
func (r *recordingRouter) Meta() tool.Tool                                        { return tool.Tool{Key: "agents", Path: "/tools/agents"} }

// TestEndpointsAreOnTheRightSide is the §23.2 table: every registered endpoint,
// and whether a logged-in non-admin may reach it.
//
// It calls each registered handler as a VIEWER and asserts only one thing —
// 403 or not. What the handler then answers (200, 501, 502 from a daemon that
// is not there) is another test's business; what matters here is the gate, and
// a wrong answer means either client data leaks or someone entitled to look is
// locked out.
func TestEndpointsAreOnTheRightSide(t *testing.T) {
	// A backend of this test's own, so the table describes routes this test
	// registered rather than whichever backends other tests happened to
	// register first (the registry is process-wide).
	const id = "gate-mem"
	Register(Descriptor{ID: id, DisplayName: id, PrefPort: 41300, HealthPath: "/healthz", Data: &fakeData{}})
	p := "/agentmemory/" + id

	// false = a viewer must be refused (admin-only).
	want := map[string]bool{
		"GET /agentmemory/backends":      true,
		"GET /agentmemory/project-scope": true,
		// Whether a project uses memory at all: anyone who may see the
		// project may read it; changing it is managing.
		"GET /agentmemory/project-policy":  true,
		"POST /agentmemory/project-policy": false,
		// Who is recording and who went quiet: a statement about the
		// host, readable by anyone who may see the panel at all.
		"GET /agentmemory/project-policies": true,

		"GET " + p + "/status":   true,
		"GET " + p + "/projects": true,
		"GET " + p + "/health":   true,
		"GET " + p + "/handoffs": true,
		"GET " + p + "/search":   true,
		"GET " + p + "/settings": true,
		"GET " + p + "/page":     true,
		"GET " + p + "/messages": true,

		// The log carries store paths and launch values, so it sits with
		// the managing half despite being a GET (PLAN §23.2).
		"GET " + p + "/logs": false,

		"POST " + p + "/start":            false,
		"POST " + p + "/stop":             false,
		"POST " + p + "/restart":          false,
		"POST " + p + "/install":          false,
		"POST " + p + "/test":             false,
		"POST " + p + "/settings":         false,
		"POST " + p + "/backfill/preview": false,
		"POST " + p + "/backfill/run":     false,
		"POST " + p + "/compact":          false,
		"POST " + p + "/handoffs/cancel":  false,
		"POST " + p + "/forget-sweep":     false,

		// Editing this project's memory (PLAN §22). All four are writes
		// in the sense that matters — they change, remove or roll back
		// what an agent will recall — and the checkpoint list is only
		// ever read next to a restore button.
		"POST " + p + "/page/write":   false,
		"POST " + p + "/page/delete":  false,
		"POST " + p + "/page/restore": false,
		"GET " + p + "/checkpoints":   false,
	}

	rr := &recordingRouter{routes: map[string]tool.HandlerFunc{}}
	prev := store
	t.Cleanup(func() { store = prev })
	RegisterRoutes(rr, &fakeStore{enabled: true, viewer: true})

	for route := range want {
		if rr.routes[route] == nil {
			t.Fatalf("%s is not registered — the table and the routes disagree", route)
		}
	}
	for route, h := range rr.routes {
		// Other backends' copies of the same endpoints are skipped: they
		// are the same handlers behind the same wrappers, and a test that
		// asserted on them would break every time one is registered.
		if !strings.Contains(route, p) && !strings.HasSuffix(route, "/agentmemory/backends") &&
			!strings.HasSuffix(route, "/agentmemory/project-scope") && !strings.HasSuffix(route, "/agentmemory/project-policy") &&
			!strings.HasSuffix(route, "/agentmemory/project-policies") {
			continue
		}
		allow, listed := want[route]
		if !listed {
			t.Fatalf("%s is registered but not classified — say which side of §23.2 it is on", route)
		}
		rec, c := get(nil)
		h(c)
		refused := rec.Code == http.StatusForbidden
		if allow == refused {
			t.Fatalf("%s: viewer got %d, want %s", route, rec.Code, map[bool]string{true: "not 403", false: "403"}[allow])
		}
	}
}

// TestStatusSeparatesDaemonFromStore: an unreadable store must not blank the
// daemon block. The two answer different questions and can disagree.
func TestStatusSeparatesDaemonFromStore(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true, set: Settings{DataDir: "/srv/mem"}})
	be := testBackend(&fakeData{status: func() (*StoreStatus, error) { return nil, ErrWebDisabled }})

	w, c := get(nil)
	statusHandler(be, c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var ov Overview
	if err := json.Unmarshal(w.Body.Bytes(), &ov); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ov.Store != nil {
		t.Fatal("store must be absent when it could not be read")
	}
	if ov.StoreReason != reasonWebDisabled {
		t.Fatalf("reason %q, want %q", ov.StoreReason, reasonWebDisabled)
	}
	if ov.Daemon.PrefPort != 49999 || ov.Daemon.State == "" {
		t.Fatalf("daemon block lost: %+v", ov.Daemon)
	}
	if ov.Settings.DataDir != "/srv/mem" {
		t.Fatalf("settings: %+v", ov.Settings)
	}
}

// TestSaveSettingsValidation covers the two inputs that would be accepted and
// then quietly do the wrong thing: a port outside the legal range, and a
// relative store path, which would resolve against wick's cwd instead of where
// the operator meant.
func TestSaveSettingsValidation(t *testing.T) {
	cases := []struct {
		name string
		form url.Values
		want int
	}{
		{"port too high", url.Values{"port": {"70000"}}, http.StatusBadRequest},
		{"relative data dir", url.Values{"data_dir": {"mem/data"}}, http.StatusBadRequest},
		{"absolute data dir", url.Values{"data_dir": {"/srv/mem"}, "port": {"49374"}}, http.StatusOK},
		{"port 0 means the backend default", url.Values{"port": {"0"}}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeStore{enabled: true, admin: true}
			withStore(t, fs)
			w, c := post(tc.form)
			saveSettings(testBackend(&fakeData{}), c)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d (%s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusOK && fs.saved == nil {
				t.Fatal("a valid save must reach the store")
			}
			if tc.want != http.StatusOK && fs.saved != nil {
				t.Fatal("a rejected save must not reach the store")
			}
		})
	}
}

// TestSaveSettingsPersistsFalse: switches must be storable as off. A save that
// only wrote non-zero values would make "turn the web API back off" impossible.
func TestSaveSettingsPersistsFalse(t *testing.T) {
	fs := &fakeStore{enabled: true, admin: true, set: Settings{EnableWeb: true, Autostart: true, DataDir: "/srv/mem"}}
	withStore(t, fs)
	w, c := post(url.Values{"enable_web": {"false"}, "autostart": {"false"}, "data_dir": {""}})
	saveSettings(testBackend(&fakeData{}), c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if fs.saved.EnableWeb || fs.saved.Autostart || fs.saved.DataDir != "" {
		t.Fatalf("falses not persisted: %+v", *fs.saved)
	}
}

// TestAutostartLockIsDerived: the lock is computed on read and never written,
// so unwiring the last instance restores what the operator had chosen.
func TestAutostartLockIsDerived(t *testing.T) {
	fs := &fakeStore{enabled: true, admin: true}
	withStore(t, fs)
	w, c := post(url.Values{"autostart": {"false"}})
	saveSettings(testBackend(&fakeData{}), c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if fs.saved.AutostartLocked {
		t.Fatal("the lock must never be stored")
	}
	// Unlocked and stored-off = off; locked = on regardless of the store.
	if (Settings{}).EffectiveAutostart() {
		t.Fatal("an unused, unset backend must not autostart")
	}
	if !(Settings{AutostartLocked: true}).EffectiveAutostart() {
		t.Fatal("an instance that uses the backend must force autostart on")
	}
}

// TestBackfillForceNeedsConfirmation is the guard that matters most in this
// feature: a real forced import is refused unless the same request confirms
// it, because observations do not dedupe and every forced run adds them again.
func TestBackfillForceNeedsConfirmation(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true, set: Settings{BackfillMaxSessions: 500}})

	fd := &fakeData{}
	w, c := post(url.Values{"force": {"true"}})
	runBackfill(testBackend(fd), c, false)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed force: status %d", w.Code)
	}
	if fd.backfills != 0 {
		t.Fatal("a refused import must not reach the backend")
	}
	if body := decodeBody(t, w); body["warning"] == "" {
		t.Fatal("the refusal must carry the consequence, not just a no")
	}

	// Confirmed, it runs — and passes force through untouched.
	w, c = post(url.Values{"force": {"true"}, "confirm": {"true"}})
	runBackfill(testBackend(fd), c, false)
	if w.Code != http.StatusOK {
		t.Fatalf("confirmed force: status %d (%s)", w.Code, w.Body.String())
	}
	if !fd.lastReq.Force || fd.lastReq.DryRun {
		t.Fatalf("request: %+v", fd.lastReq)
	}
}

// TestBackfillPreviewIsAlwaysDry: the preview endpoint cannot import, even
// when the caller asks for force — that is what makes it safe to click.
func TestBackfillPreviewIsAlwaysDry(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	fd := &fakeData{}
	w, c := post(url.Values{"force": {"true"}})
	runBackfill(testBackend(fd), c, true)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !fd.lastReq.DryRun {
		t.Fatalf("preview must be a dry run: %+v", fd.lastReq)
	}
}

// TestBackfillCapDefaultsToWicks: an unset cap must not inherit the backend's
// default of 25, which silently truncates a long history.
func TestBackfillCapDefaultsToWicks(t *testing.T) {
	cases := []struct {
		name string
		set  Settings
		form url.Values
		want int
	}{
		{"nothing configured", Settings{}, nil, DefaultBackfillMaxSessions},
		{"configured default", Settings{BackfillMaxSessions: 500}, nil, 500},
		{"explicit per-run value wins", Settings{BackfillMaxSessions: 500}, url.Values{"max_sessions": {"12"}}, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withStore(t, &fakeStore{enabled: true, admin: true, set: tc.set})
			fd := &fakeData{}
			_, c := post(tc.form)
			runBackfill(testBackend(fd), c, true)
			if fd.lastReq.MaxSessions != tc.want {
				t.Fatalf("max sessions %d, want %d", fd.lastReq.MaxSessions, tc.want)
			}
		})
	}
}

// TestUnknownScopeDirIsRefused: ai-memory resolves an unknown directory to
// some OTHER project's scope instead of failing, so wick checks the directory
// before handing it over (PLAN §14).
func TestUnknownScopeDirIsRefused(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	fd := &fakeData{}
	w, c := post(url.Values{"dir": {"/no/such/folder/here"}})
	runBackfill(testBackend(fd), c, true)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
	if fd.backfills != 0 {
		t.Fatal("an unresolvable scope must not reach the backend")
	}
}

// TestScopeDirIsPassedThrough: a real directory reaches the backend as the
// working directory of the command.
func TestScopeDirIsPassedThrough(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	dir := t.TempDir()
	fd := &fakeData{}
	w, c := post(url.Values{"dir": {dir}, "project": {"wick-8c28230d"}, "workspace": {"qiscus"}})
	runBackfill(testBackend(fd), c, true)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if fd.lastReq.Scope.Dir != dir || fd.lastReq.Scope.Project != "wick-8c28230d" || fd.lastReq.Scope.Workspace != "qiscus" {
		t.Fatalf("scope: %+v", fd.lastReq.Scope)
	}
}

// TestSearchAlwaysExplainsTokenMatching: whole-token matching is the reason a
// search for "kasir" finds nothing that "kasir_prod_db" finds, so the note
// rides along with every answer — including the empty one.
func TestSearchAlwaysExplainsTokenMatching(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	be := testBackend(&fakeData{search: func(_ ReadScope, q string) ([]SearchHit, error) {
		return []SearchHit{{Path: "p.md", Title: q}}, nil
	}})

	w, c := get(url.Values{"q": {"kasir_prod_db"}})
	searchHandler(be, c)
	body := decodeBody(t, w)
	if body["note"] == "" || !strings.Contains(body["note"].(string), "kasir_prod_db") {
		t.Fatalf("note: %v", body["note"])
	}
	if hits, ok := body["hits"].([]any); !ok || len(hits) != 1 {
		t.Fatalf("hits: %v", body["hits"])
	}

	// An empty query short-circuits, but still explains itself.
	w, c = get(nil)
	searchHandler(be, c)
	body = decodeBody(t, w)
	if body["note"] == "" {
		t.Fatal("the empty answer must explain matching too")
	}
	if hits, ok := body["hits"].([]any); !ok || len(hits) != 0 {
		t.Fatalf("empty query must answer an empty LIST, not null: %v", body["hits"])
	}
}

// TestFixableStatesAnswer200WithAReason: a disabled web API and a stopped
// daemon are ordinary states with a known fix, so they carry a machine-
// checkable reason and a hint instead of an error the FE renders as a crash.
func TestFixableStatesAnswer200WithAReason(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	be := testBackend(&fakeData{projects: func() ([]ProjectRow, error) { return nil, ErrWebDisabled }})

	w, c := get(nil)
	projectsHandler(be, c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["reason"] != reasonWebDisabled {
		t.Fatalf("reason: %v", body["reason"])
	}
	if body["hint"] == "" {
		t.Fatal("a fixable state must say how to fix it")
	}
}

// TestBackendWithoutDataSourceIsNotAnEmptyStore: a backend that exposes no
// panel data answers 501, so "not implemented" is never rendered as "you have
// nothing stored".
func TestBackendWithoutDataSourceIsNotAnEmptyStore(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	w, c := get(nil)
	projectsHandler(testBackend(nil), c)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status %d", w.Code)
	}
}

// TestProjectsAnswerIsAListNotNull keeps the FE off a null check: an empty
// store answers [].
func TestProjectsAnswerIsAListNotNull(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	w, c := get(nil)
	projectsHandler(testBackend(&fakeData{}), c)
	if got := strings.TrimSpace(w.Body.String()); got != `{"projects":[]}` {
		t.Fatalf("body %s", got)
	}
}

// fakeBriefer is a DataSource that can also brief, so the other optional
// interface is exercised the way a real backend offers it. brief decides per
// project what the backend "answered" — that is the whole point of the
// fan-out: the rows do not share a fate.
type fakeBriefer struct {
	*fakeData
	brief func(ReadScope) (*ProjectBriefing, error)
	mu    sync.Mutex
	asked []ReadScope
}

func (f *fakeBriefer) ProjectBriefing(_ context.Context, _ Conn, s ReadScope) (*ProjectBriefing, error) {
	f.mu.Lock()
	f.asked = append(f.asked, s)
	f.mu.Unlock()
	if f.brief != nil {
		return f.brief(s)
	}
	return &ProjectBriefing{}, nil
}

// threeProjects is the fake listing the briefing tests fan out over.
func threeProjects() *fakeData {
	return &fakeData{projects: func() ([]ProjectRow, error) {
		return []ProjectRow{
			{Workspace: "default", Project: "proj2", PageCount: 6},
			{Workspace: "default", Project: "proj", PageCount: 1},
			{Workspace: "default", Project: "scratch"},
		}, nil
	}}
}

// TestProjectsCarryTheirOwnBriefing: each row is briefed with ITS OWN scope.
// The backend resolves an unnamed scope from elsewhere, so a fan-out that
// forgot to name the project would hand every row the same numbers — which
// looks like data rather than like a bug.
func TestProjectsCarryTheirOwnBriefing(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	br := &fakeBriefer{fakeData: threeProjects(), brief: func(s ReadScope) (*ProjectBriefing, error) {
		return &ProjectBriefing{Counts: BriefingCounts{Sessions: int64(len(s.Project))}}, nil
	}}

	w, c := get(nil)
	projectsHandler(testBackend(br), c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Projects []ProjectRow `json:"projects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Projects) != 3 {
		t.Fatalf("rows: %+v", got.Projects)
	}
	for _, r := range got.Projects {
		if r.Briefing == nil {
			t.Fatalf("%s has no briefing", r.Project)
		}
		if r.Briefing.Counts.Sessions != int64(len(r.Project)) {
			t.Fatalf("%s got another project's numbers: %+v", r.Project, r.Briefing.Counts)
		}
	}
	if len(br.asked) != 3 {
		t.Fatalf("one call per project, got %d", len(br.asked))
	}
	for _, s := range br.asked {
		if s.Workspace == "" || s.Project == "" {
			t.Fatalf("an unscoped briefing was sent: %+v", s)
		}
	}
}

// TestOneFailedBriefingDoesNotBlankTheRest is the isolation rule. A project
// renamed out from under the listing, or a daemon that dies mid-fan-out, must
// cost ITS row its numbers and nothing more — not the other rows', and not
// the project list the page is really about.
func TestOneFailedBriefingDoesNotBlankTheRest(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	br := &fakeBriefer{fakeData: threeProjects(), brief: func(s ReadScope) (*ProjectBriefing, error) {
		if s.Project == "proj" {
			return nil, errors.New("project 'proj' not found in workspace 'default'")
		}
		return &ProjectBriefing{Counts: BriefingCounts{Sessions: 6}}, nil
	}}

	w, c := get(nil)
	projectsHandler(testBackend(br), c)
	if w.Code != http.StatusOK {
		t.Fatalf("a failed briefing took the whole page down: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Projects []ProjectRow `json:"projects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := map[string]ProjectRow{}
	for _, r := range got.Projects {
		byName[r.Project] = r
	}
	bad := byName["proj"]
	// Absent, NOT zeroed: a project whose counters could not be read has not
	// been shown to have none.
	if bad.Briefing != nil {
		t.Fatalf("a failed briefing must be absent, got %+v", bad.Briefing)
	}
	if !strings.Contains(bad.BriefingError, "not found in workspace") {
		t.Fatalf("the backend's message must survive: %q", bad.BriefingError)
	}
	if byName["proj2"].Briefing == nil || byName["scratch"].Briefing == nil {
		t.Fatal("one row's failure blanked the others")
	}
}

// TestProjectsWithoutABriefer: a backend that cannot brief still lists its
// projects. The rows simply carry no briefing, which the FE renders as a
// stated absence rather than as zeros.
func TestProjectsWithoutABriefer(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	w, c := get(nil)
	projectsHandler(testBackend(threeProjects()), c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "briefing") {
		t.Fatalf("a non-briefing backend invented a briefing: %s", w.Body.String())
	}
}

// fakeCompactor is a DataSource that can also compact, so the optional
// interface is exercised the way a real backend offers it.
type fakeCompactor struct {
	*fakeData
	calls int
}

func (f *fakeCompactor) Compact(context.Context, Conn) (*CompactReport, error) {
	f.calls++
	return &CompactReport{Output: "Compacted: 1.0 MiB → 1020.0 KiB (24.0 KiB reclaimed)."}, nil
}

// TestCompactNeedsConfirm: compaction blocks every write for the length of a
// full database rewrite, so the fence lives on the endpoint — an unconfirmed
// call must not reach the backend at all, and the refusal must carry the
// consequence rather than just the word "confirm".
func TestCompactNeedsConfirm(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	cp := &fakeCompactor{fakeData: &fakeData{}}
	be := testBackend(cp)

	w, c := post(url.Values{})
	compactHandler(be, c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed compact: want 400, got %d", w.Code)
	}
	if cp.calls != 0 {
		t.Fatalf("backend must not be reached without confirm, got %d calls", cp.calls)
	}
	if body := decodeBody(t, w); !strings.Contains(body["warning"].(string), "exclusive lock") {
		t.Fatalf("refusal must state the consequence, got %v", body["warning"])
	}

	w, c = post(url.Values{"confirm": {"true"}})
	compactHandler(be, c)
	if w.Code != http.StatusOK || cp.calls != 1 {
		t.Fatalf("confirmed compact: code=%d calls=%d", w.Code, cp.calls)
	}
}

// TestCompactUnsupportedBackendIsNotASilentNoOp: a backend that cannot compact
// says so with 501. Answering 200 would leave the user watching "reclaimable"
// never move with nothing to explain it.
func TestCompactUnsupportedBackendIsNotASilentNoOp(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	w, c := post(url.Values{"confirm": {"true"}})
	compactHandler(testBackend(&fakeData{}), c)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d: %s", w.Code, w.Body.String())
	}
}

// TestProjectScopeHandler: the panel asks the SERVER which bucket a wick
// project uses, and the two failures it can meet are told apart. A missing
// parameter is the caller's bug; an unknown id is a project that is gone —
// answering either with some default scope would open the panel on another
// project's memory (PLAN §22.2).
func TestProjectScopeHandler(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, viewer: true})
	dir := t.TempDir()
	withProjects(t, "wick", ProjectFolder{ID: "8c28230d-aaaa", Name: "Kasir", Folder: dir})

	t.Run("resolves", func(t *testing.T) {
		w, c := get(url.Values{"project": {"8c28230d-aaaa"}})
		projectScopeHandler(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var got projectScope
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Workspace != "wick" || got.Project != "kasir-8c28230d" {
			t.Fatalf("scope %s/%s", got.Workspace, got.Project)
		}
		if got.Source != string(ScopeFromWick) || got.Folder != dir || got.Name != "Kasir" {
			t.Fatalf("payload %+v", got)
		}
	})

	t.Run("missing param", func(t *testing.T) {
		w, c := get(nil)
		projectScopeHandler(c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400", w.Code)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		w, c := get(url.Values{"project": {"nobody"}})
		projectScopeHandler(c)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status %d, want 404", w.Code)
		}
	})
}
