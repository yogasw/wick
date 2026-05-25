package repository

import (
	"testing"

	wf "github.com/yogasw/wick/internal/agents/workflow"
)

type fakeFileSvc struct {
	wfs    map[string]wf.Workflow
	drafts map[string]wf.Workflow
}

func (f *fakeFileSvc) List() ([]string, error) {
	out := make([]string, 0, len(f.wfs))
	for id := range f.wfs {
		out = append(out, id)
	}
	return out, nil
}

func (f *fakeFileSvc) Load(id string) (wf.Workflow, error) {
	w, ok := f.wfs[id]
	if !ok {
		return wf.Workflow{}, errNotFound
	}
	return w, nil
}

func (f *fakeFileSvc) HasDraft(id string) bool { _, ok := f.drafts[id]; return ok }

func (f *fakeFileSvc) LoadDraft(id string) (wf.Workflow, error) {
	w, ok := f.drafts[id]
	if !ok {
		return f.Load(id)
	}
	return w, nil
}

var errNotFound = newError("not found")

func newError(s string) error { return &simpleErr{s} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }

// TestImportFromFilesSeedsRowsAndVersions confirms each file workflow
// lands in the DB with one published snapshot.
func TestImportFromFilesSeedsRowsAndVersions(t *testing.T) {
	r := New(openMem(t))
	svc := &fakeFileSvc{
		wfs: map[string]wf.Workflow{
			"alpha": sampleWorkflow("alpha"),
			"beta":  sampleWorkflow("beta"),
		},
	}
	n, err := r.ImportFromFiles(svc)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 2 {
		t.Errorf("imported: got %d want 2", n)
	}
	rows, _ := r.List()
	if len(rows) != 2 {
		t.Errorf("rows: got %d want 2", len(rows))
	}
	for _, row := range rows {
		vs, _ := r.Versions(row.ID)
		if len(vs) != 1 {
			t.Errorf("%s: versions %d want 1 published anchor", row.ID, len(vs))
		}
		if vs[0].Kind != "published" {
			t.Errorf("%s: first version kind %s want published", row.ID, vs[0].Kind)
		}
	}
}

// TestImportIsIdempotent ensures a second run doesn't duplicate.
func TestImportIsIdempotent(t *testing.T) {
	r := New(openMem(t))
	svc := &fakeFileSvc{wfs: map[string]wf.Workflow{"x": sampleWorkflow("x")}}
	if _, err := r.ImportFromFiles(svc); err != nil {
		t.Fatalf("first import: %v", err)
	}
	n, err := r.ImportFromFiles(svc)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if n != 0 {
		t.Errorf("second import touched %d rows, want 0", n)
	}
	rows, _ := r.List()
	if len(rows) != 1 {
		t.Errorf("dup rows: got %d want 1", len(rows))
	}
}

// TestImportCarriesDraft also persists the in-flight draft when the
// file store has one.
func TestImportCarriesDraft(t *testing.T) {
	r := New(openMem(t))
	w := sampleWorkflow("d")
	d := sampleWorkflow("d")
	d.Name = "drafty"
	svc := &fakeFileSvc{
		wfs:    map[string]wf.Workflow{"d": w},
		drafts: map[string]wf.Workflow{"d": d},
	}
	if _, err := r.ImportFromFiles(svc); err != nil {
		t.Fatalf("import: %v", err)
	}
	row, _ := r.Get("d")
	if !row.HasDraft {
		t.Error("draft flag not set after import")
	}
	if row.YAMLDraft == "" {
		t.Error("draft yaml empty after import")
	}
	vs, _ := r.Versions("d")
	if len(vs) != 2 {
		t.Errorf("versions: got %d want 2 (published + draft)", len(vs))
	}
}
