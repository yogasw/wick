package agentmemory

import (
	"os"
	"path/filepath"
	"testing"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeMarker(t *testing.T, dir, ws, proj string) {
	t.Helper()
	body := ""
	if ws != "" {
		body += "workspace = \"" + ws + "\"\n"
	}
	if proj != "" {
		body += "project = \"" + proj + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, MarkerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The failure with no other symptom: two unrelated projects whose folders are
// both called "backend" become ONE ai-memory project, and each client's
// sessions are recalled into the other's.
func TestTwoUnmarkedFoldersSharingABasenameCollide(t *testing.T) {
	root := t.TempDir()
	a := mkdir(t, root, "clientA", "backend")
	b := mkdir(t, root, "clientB", "backend")

	got := DetectCollisions("wick", []ProjectFolder{
		{ID: "id-a", Name: "Client A backend", Folder: a, CustomPath: true},
		{ID: "id-b", Name: "Client B backend", Folder: b, CustomPath: true},
	})
	if len(got) != 1 {
		t.Fatalf("want 1 collision, got %d: %+v", len(got), got)
	}
	if got[0].Project != "backend" {
		t.Fatalf("collision is on the basename: %+v", got[0])
	}
	if len(got[0].Members) != 2 {
		t.Fatalf("both projects belong to it: %+v", got[0].Members)
	}
	// Writing a marker into either one separates them, so the finding says so.
	if !got[0].Fixable {
		t.Fatal("a collision between unmarked folders is fixable by writing a marker")
	}
}

// The marker is the fix, so it has to actually prevent the finding.
func TestAMarkerSeparatesTwoSameNamedFolders(t *testing.T) {
	root := t.TempDir()
	a := mkdir(t, root, "clientA", "backend")
	b := mkdir(t, root, "clientB", "backend")
	writeMarker(t, a, "wick", "client-a-backend-1234abcd")
	writeMarker(t, b, "wick", "client-b-backend-5678efgh")

	if got := DetectCollisions("wick", []ProjectFolder{
		{ID: "a", Name: "A", Folder: a},
		{ID: "b", Name: "B", Folder: b},
	}); len(got) != 0 {
		t.Fatalf("markers must separate them, got %+v", got)
	}
}

// Two markers naming the same scope is a human decision, not wick's to undo —
// it is still reported, but not as something a marker would fix.
func TestTwoMarkersNamingOneScopeAreReportedButNotFixable(t *testing.T) {
	root := t.TempDir()
	a := mkdir(t, root, "one")
	b := mkdir(t, root, "two")
	writeMarker(t, a, "wick", "shared")
	writeMarker(t, b, "wick", "shared")

	got := DetectCollisions("wick", []ProjectFolder{{ID: "a", Name: "A", Folder: a}, {ID: "b", Name: "B", Folder: b}})
	if len(got) != 1 {
		t.Fatalf("want 1 collision, got %+v", got)
	}
	if got[0].Fixable {
		t.Fatal("both are marked on purpose — wick must not offer to overwrite a scope a human pinned")
	}
}

// The hook takes the CLOSEST marker walking up, so two sibling checkouts under
// one marked parent both land in the parent's project. A check that only
// looked in the folder itself would call that two distinct projects.
func TestAParentMarkerGovernsItsChildren(t *testing.T) {
	root := t.TempDir()
	parent := mkdir(t, root, "monorepo")
	writeMarker(t, parent, "wick", "monorepo-aaaa1111")
	a := mkdir(t, parent, "api")
	b := mkdir(t, parent, "web")

	got := DetectCollisions("wick", []ProjectFolder{{ID: "a", Name: "api", Folder: a}, {ID: "b", Name: "web", Folder: b}})
	if len(got) != 1 || got[0].Project != "monorepo-aaaa1111" {
		t.Fatalf("both children resolve to the parent's scope, got %+v", got)
	}
	// Neither child carries its own marker, so writing one would split them.
	for _, m := range got[0].Members {
		if m.HasMarker {
			t.Fatalf("child %s has no marker of its own", m.Name)
		}
	}
}

func TestDistinctBasenamesDoNotCollide(t *testing.T) {
	root := t.TempDir()
	a := mkdir(t, root, "alpha")
	b := mkdir(t, root, "beta")
	if got := DetectCollisions("wick", []ProjectFolder{{ID: "a", Folder: a}, {ID: "b", Folder: b}}); len(got) != 0 {
		t.Fatalf("no collision expected, got %+v", got)
	}
}

// An unmarked folder lands in the install's workspace, the same one a marked
// folder would — otherwise the check would compare two different workspaces
// and miss the overlap entirely.
func TestUnmarkedFoldersUseTheInstallWorkspace(t *testing.T) {
	root := t.TempDir()
	a := mkdir(t, root, "x", "svc")
	b := mkdir(t, root, "y", "svc")
	got := DetectCollisions("My Wick Host", []ProjectFolder{{ID: "a", Folder: a}, {ID: "b", Folder: b}})
	if len(got) != 1 {
		t.Fatalf("want 1 collision, got %+v", got)
	}
	if got[0].Workspace != "my-wick-host" {
		t.Fatalf("workspace must be sanitised the same way the marker writer does it: %q", got[0].Workspace)
	}
}

func TestEmptyFolderIsSkippedRatherThanGuessedAt(t *testing.T) {
	if got := DetectCollisions("wick", []ProjectFolder{{ID: "a", Folder: ""}, {ID: "b", Folder: "   "}}); len(got) != 0 {
		t.Fatalf("a project with no folder tells us nothing, got %+v", got)
	}
}

// "Not checked" must never render as "no collisions" — that is the difference
// between a clean bill of health and an unanswered question.
func TestCollisionCheckWithoutAListerSaysSoRatherThanReportingClean(t *testing.T) {
	prev := projectLister
	t.Cleanup(func() { projectLister = prev })
	projectLister = nil

	got := RunCollisionCheck()
	if got.Checked {
		t.Fatal("nothing was compared, so Checked must be false")
	}
	if got.Reason == "" {
		t.Fatal("an unchecked result has to say why")
	}
}

func TestRunCollisionCheckCountsWhatItScanned(t *testing.T) {
	root := t.TempDir()
	a := mkdir(t, root, "p", "files")
	b := mkdir(t, root, "q", "files")
	prev, prevWS := projectLister, markerWorkspace
	t.Cleanup(func() { projectLister, markerWorkspace = prev, prevWS })
	SetMarkerWorkspace("wick")
	SetProjectLister(func() []ProjectFolder {
		return []ProjectFolder{{ID: "a", Name: "A", Folder: a}, {ID: "b", Name: "B", Folder: b}}
	})

	got := RunCollisionCheck()
	if !got.Checked || got.Scanned != 2 {
		t.Fatalf("scanned count wrong: %+v", got)
	}
	// projects/<uuid>/files is wick's own layout, so "files" is the collision
	// every unmarked wick project falls into (PLAN §14.4).
	if len(got.Collisions) != 1 || got.Collisions[0].Project != "files" {
		t.Fatalf("want the 'files' collision, got %+v", got.Collisions)
	}
}
