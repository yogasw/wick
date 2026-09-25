package agentmemory

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/yogasw/wick/pkg/tool"
)

// The editing endpoints' own rules (PLAN §22, §23).
//
// What these pin down is everything that happens BEFORE the backend is
// called: an edit that reaches the CLI without an explicit scope is an edit
// filed in whichever project the daemon resolved on its own, and a delete
// that reaches it without a confirmation is a page gone on one stray click.

// manageData is a DataSource that also implements the four editing
// interfaces, recording what it was asked to do.
type manageData struct {
	fakeData
	wrote    PageWrite
	scope    ReadScope
	deleted  string
	restored [2]string
	limit    int
	err      error
}

func (m *manageData) WritePage(_ context.Context, _ Conn, s ReadScope, w PageWrite) (*PageWriteResult, error) {
	m.scope, m.wrote = s, w
	if m.err != nil {
		return nil, m.err
	}
	return &PageWriteResult{Path: w.Path, PageID: "01a0d8dd", Output: "✓ wrote " + w.Path + " under " + s.Workspace + "/" + s.Project}, nil
}

func (m *manageData) DeletePage(_ context.Context, _ Conn, s ReadScope, path string) (*PageDeleteResult, error) {
	m.scope, m.deleted = s, path
	if m.err != nil {
		return nil, m.err
	}
	return &PageDeleteResult{Path: path, Deleted: true, Output: "✓ deleted " + path + " under " + s.Workspace + "/" + s.Project}, nil
}

func (m *manageData) Checkpoints(_ context.Context, _ Conn, limit int) ([]Checkpoint, error) {
	m.limit = limit
	if m.err != nil {
		return nil, m.err
	}
	return []Checkpoint{{OID: "d544bc66", ShortOID: "d544bc66", TimeUnix: 1790344784, Summary: "write-page wick/demo: notes/hello.md"}}, nil
}

func (m *manageData) RestorePage(_ context.Context, _ Conn, s ReadScope, path, from string) (*RestoreResult, error) {
	m.scope, m.restored = s, [2]string{path, from}
	if m.err != nil {
		return nil, m.err
	}
	return &RestoreResult{PageID: "01a0d8dd", Path: path, RestoredFrom: from, Checkpoint: "25d150d1"}, nil
}

// scopedForm is the form every editing call carries.
func scopedForm(extra url.Values) url.Values {
	v := url.Values{"workspace": {"wick"}, "project": {"demo"}}
	for k, vals := range extra {
		v[k] = vals
	}
	return v
}

func manageBackend(d DataSource) *Backend {
	desc := Descriptor{ID: "manage-mem", DisplayName: "manage-mem", BinName: "manage-mem", PrefPort: 42200, HealthPath: "/healthz", Data: d}
	return &Backend{Desc: desc, Mgr: newManager(desc)}
}

// TestWritePageNeedsAnExplicitScope: the backend resolves an unnamed scope
// from a working directory, so a write without one lands in whatever project
// the daemon guessed — a wrong fact stored in someone else's memory.
func TestWritePageNeedsAnExplicitScope(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	d := &manageData{}

	for _, form := range []url.Values{
		{"path": {"notes/x.md"}, "body": {"hello"}},
		{"workspace": {"wick"}, "path": {"notes/x.md"}, "body": {"hello"}},
		{"project": {"demo"}, "path": {"notes/x.md"}, "body": {"hello"}},
	} {
		w, c := post(form)
		writePageHandler(manageBackend(d), c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d for %v, want 400", w.Code, form)
		}
	}
	if d.wrote.Path != "" {
		t.Fatal("an unscoped write reached the backend")
	}
}

// TestWritePageSaves: the happy path, and the note that says the edit is
// recoverable — the sentence people need next to an edit box.
func TestWritePageSaves(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	d := &manageData{}
	w, c := post(scopedForm(url.Values{
		"path": {"notes/hello.md"}, "body": {"# Hello\n\nkasir_prod_db is a replica."},
		"title": {"Hello"}, "kind": {"fact"},
	}))
	writePageHandler(manageBackend(d), c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if d.scope.Workspace != "wick" || d.scope.Project != "demo" {
		t.Fatalf("scope %+v", d.scope)
	}
	if d.wrote.Title != "Hello" || d.wrote.Kind != "fact" || d.wrote.Path != "notes/hello.md" {
		t.Fatalf("write %+v", d.wrote)
	}
	body := decodeBody(t, w)
	if note, _ := body["note"].(string); note == "" {
		t.Fatal("the reassurance that an edit is restorable must travel with the edit")
	}
	res, ok := body["result"].(map[string]any)
	if !ok || res["path"] != "notes/hello.md" {
		t.Fatalf("result %v", body["result"])
	}
}

// TestWritePageRefusesAnEmptyBody: clearing the box is a delete, and a delete
// has to say what it removes.
func TestWritePageRefusesAnEmptyBody(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	d := &manageData{}
	w, c := post(scopedForm(url.Values{"path": {"notes/hello.md"}, "body": {"   \n"}}))
	writePageHandler(manageBackend(d), c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
	if d.wrote.Path != "" {
		t.Fatal("an empty body reached the backend")
	}
}

// TestDeletePageNeedsConfirmation: the fence lives with the action, not only
// in whichever FE calls it.
func TestDeletePageNeedsConfirmation(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	d := &manageData{}
	w, c := post(scopedForm(url.Values{"path": {"notes/hello.md"}}))
	deletePageHandler(manageBackend(d), c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
	if d.deleted != "" {
		t.Fatal("an unconfirmed delete reached the backend")
	}
	body := decodeBody(t, w)
	if warn, _ := body["warning"].(string); warn == "" {
		t.Fatal("the refusal must carry the consequence, not just the rule")
	}
}

// TestDeletePageNamesWhatItRemoved: the answer says which page went, and
// that it is still in git.
func TestDeletePageNamesWhatItRemoved(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	d := &manageData{}
	w, c := post(scopedForm(url.Values{"path": {"notes/hello.md"}, "confirm": {"true"}}))
	deletePageHandler(manageBackend(d), c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if d.deleted != "notes/hello.md" {
		t.Fatalf("deleted %q", d.deleted)
	}
	body := decodeBody(t, w)
	res, _ := body["result"].(map[string]any)
	if res["path"] != "notes/hello.md" || res["deleted"] != true {
		t.Fatalf("result %v", body["result"])
	}
	if warn, _ := body["warning"].(string); warn == "" {
		t.Fatal("no warning on the answer")
	}
}

// TestCheckpointsAndRestore: the way back from a bad edit, end to end.
func TestCheckpointsAndRestore(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	d := &manageData{}
	be := manageBackend(d)

	w, c := get(url.Values{"limit": {"5"}})
	checkpointsHandler(be, c)
	if w.Code != http.StatusOK {
		t.Fatalf("checkpoints: %d", w.Code)
	}
	if d.limit != 5 {
		t.Fatalf("limit %d", d.limit)
	}
	rows, _ := decodeBody(t, w)["checkpoints"].([]any)
	if len(rows) != 1 {
		t.Fatalf("checkpoints %v", rows)
	}

	w, c = post(scopedForm(url.Values{"path": {"notes/hello.md"}, "from": {"d544bc66"}}))
	restorePageHandler(be, c)
	if w.Code != http.StatusOK {
		t.Fatalf("restore: %d — %s", w.Code, w.Body.String())
	}
	if d.restored != [2]string{"notes/hello.md", "d544bc66"} {
		t.Fatalf("restored %v", d.restored)
	}
	body := decodeBody(t, w)
	res, _ := body["result"].(map[string]any)
	if res["path"] != "notes/hello.md" || res["checkpoint"] == "" {
		t.Fatalf("result %v", body["result"])
	}
	// The restore made its own checkpoint, and says so: undoing an undo is
	// the next thing someone wants.
	if note, _ := body["note"].(string); note == "" {
		t.Fatal("no note about the restore's own checkpoint")
	}
}

// TestRestoreNeedsBothHalves: a path with no checkpoint (or the reverse) is
// a request the backend would answer by guessing.
func TestRestoreNeedsBothHalves(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	d := &manageData{}
	for _, form := range []url.Values{
		scopedForm(url.Values{"path": {"notes/hello.md"}}),
		scopedForm(url.Values{"from": {"d544bc66"}}),
	} {
		w, c := post(form)
		restorePageHandler(manageBackend(d), c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d for %v, want 400", w.Code, form)
		}
	}
}

// TestManageEndpointsOnABackendThatCannotEdit: a backend without the
// interfaces says so, rather than failing deeper with a message about a
// missing CLI flag.
func TestManageEndpointsOnABackendThatCannotEdit(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	be := manageBackend(&fakeData{}) // the plain DataSource: reads only

	for _, tc := range []struct {
		name string
		fn   func(*Backend, *tool.Ctx)
		form url.Values
	}{
		{"write", writePageHandler, scopedForm(url.Values{"path": {"x.md"}, "body": {"y"}})},
		{"delete", deletePageHandler, scopedForm(url.Values{"path": {"x.md"}, "confirm": {"true"}})},
		{"checkpoints", checkpointsHandler, nil},
		{"restore", restorePageHandler, scopedForm(url.Values{"path": {"x.md"}, "from": {"abc"}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, c := post(tc.form)
			tc.fn(be, c)
			if w.Code != http.StatusNotImplemented {
				t.Fatalf("status %d, want 501", w.Code)
			}
			if msg, _ := decodeBody(t, w)["error"].(string); msg == "" {
				t.Error("501 with no explanation")
			}
		})
	}
}

// TestManageEndpointsRefuseAViewer: the §23 split, on the endpoints that can
// change what an agent recalls. Driven through the registered routes so the
// wrapper each one was registered with is what is tested.
func TestManageEndpointsRefuseAViewer(t *testing.T) {
	const id = "viewer-manage-mem"
	Register(Descriptor{ID: id, DisplayName: id, BinName: id, PrefPort: 42300, HealthPath: "/healthz", Data: &manageData{}})
	rr := &recordingRouter{routes: map[string]tool.HandlerFunc{}}
	prev := store
	t.Cleanup(func() { store = prev })
	RegisterRoutes(rr, &fakeStore{enabled: true, viewer: true})
	p := "/agentmemory/" + id

	for _, route := range []string{
		"POST " + p + "/page/write",
		"POST " + p + "/page/delete",
		"POST " + p + "/page/restore",
		"GET " + p + "/checkpoints",
	} {
		h, ok := rr.routes[route]
		if !ok {
			t.Fatalf("%s is not registered", route)
		}
		w, c := post(scopedForm(url.Values{"path": {"notes/hello.md"}, "body": {"x"}, "confirm": {"true"}, "from": {"abc"}}))
		h(c)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: a viewer got %d, want 403", route, w.Code)
		}
	}
}

// TestWriteFailureCarriesTheReason: a failed edit answers with the same
// DataFailure shape every other endpoint uses — error, reason, hint — rather
// than inventing a second error convention for the editing half.
//
// It is NOT a 200 that looks like a success: the payload carries no result,
// and `error` is what the editor renders. A daemon that is down is the most
// likely reason an edit does not save, and naming it is the difference
// between "it did not work" and "start the daemon".
func TestWriteFailureCarriesTheReason(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})
	d := &manageData{err: errors.New("ai-memory write-page: project 'demo' not found in workspace 'wick'")}
	w, c := post(scopedForm(url.Values{"path": {"notes/hello.md"}, "body": {"x"}}))
	writePageHandler(manageBackend(d), c)

	body := decodeBody(t, w)
	if msg, _ := body["error"].(string); msg == "" {
		t.Fatal("no message for the editor to show")
	}
	if _, ok := body["result"]; ok {
		t.Fatal("a failed write must not carry a result — that is what the editor reads as saved")
	}
	if reason, _ := body["reason"].(string); reason == "" {
		t.Error("the fixable reason is missing")
	}
}
