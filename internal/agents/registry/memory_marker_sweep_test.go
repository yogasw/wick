package registry

import "testing"

// recordingMarker stands in for the Agent Memory writer.
type recordingMarker struct {
	seen map[string]string // folder -> project name
	fail map[string]bool
}

func (r *recordingMarker) Ensure(folder, projectName, projectID string) error {
	if r.fail[projectID] {
		return errFake
	}
	if r.seen == nil {
		r.seen = map[string]string{}
	}
	r.seen[folder] = projectName
	return nil
}

type fakeErr struct{}

func (fakeErr) Error() string { return "marker write refused" }

var errFake = fakeErr{}

// Every wick-managed project folder is called "files" — projects/<uuid>/files
// — and ai-memory derives the project from the folder's NAME. So a host whose
// projects were created before Agent Memory was switched on has every one of
// them resolving to the same bucket: 40 projects recalling each other's work,
// including across clients. Create and move are the only per-project writes,
// and an existing project passes through neither.
func TestEnsureMemoryMarkersCoversProjectsThatAlreadyExisted(t *testing.T) {
	m, mk := managerWithProjects(t, "p1", "p2", "p3")

	if got := m.EnsureMemoryMarkers(); got != 3 {
		t.Fatalf("swept %d projects, want 3", got)
	}
	for _, id := range []string{"p1", "p2", "p3"} {
		folder := m.reg.layout.ProjectManagedPath(id)
		if _, ok := mk.seen[folder]; !ok {
			t.Errorf("project %s was left unpinned — it keeps sharing the basename bucket", id)
		}
	}
	// Distinct folders, so distinct scopes: the whole point.
	if len(mk.seen) != 3 {
		t.Fatalf("marked %d distinct folders, want 3", len(mk.seen))
	}
}

// One bad project must not stop the rest. A sweep that aborts halfway leaves
// an arbitrary subset pinned and the others silently still colliding.
func TestEnsureMemoryMarkersStepsOverAFailure(t *testing.T) {
	m, mk := managerWithProjects(t, "p1", "p2", "p3")
	mk.fail = map[string]bool{"p2": true}

	m.EnsureMemoryMarkers()
	if len(mk.seen) != 2 {
		t.Fatalf("marked %d folders, want the 2 that did not fail", len(mk.seen))
	}
}

// With no writer wired the feature is off, and an off feature drops no
// dotfiles into anybody's folders.
func TestEnsureMemoryMarkersDoesNothingWhenUnwired(t *testing.T) {
	m, _ := managerWithProjects(t, "p1")
	m.MemoryMarker = nil
	if got := m.EnsureMemoryMarkers(); got != 0 {
		t.Fatalf("swept %d with no writer, want 0", got)
	}
}

func managerWithProjects(t *testing.T, ids ...string) (*Manager, *recordingMarker) {
	t.Helper()
	layout := newLayout(t)
	for _, id := range ids {
		mkProject(t, layout, id, id)
	}
	reg := New(layout)
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}
	mk := &recordingMarker{}
	return &Manager{reg: reg, MemoryMarker: mk}, mk
}
