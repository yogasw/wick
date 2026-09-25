package agentmemory

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Project-collision detection (PLAN §14.3, §18.1).
//
// The failure this finds has no other symptom. ai-memory derives a session's
// project from the BASENAME of its working directory unless a .ai-memory.toml
// marker overrides it. So two unrelated wick projects whose folders are both
// called "backend" silently become ONE ai-memory project, and everything one
// client's sessions learned is recalled into the other's. Nothing in either
// project looks wrong; the store looks healthy; the only visible trace is
// memory that knows too much.
//
// wick writes a marker with a uuid suffix for the projects it manages, which
// is what prevents this. But it deliberately does NOT write one into a
// CUSTOM-PATH project — that folder belongs to the user, often a git repo, and
// dropping a dotfile into it is not wick's call (registry.ensureMemoryMarker).
// Those are exactly the projects that can collide, which is why this check
// exists rather than being assumed away by the marker writer.
//
// Everything here is computed locally from wick's own project list and the
// markers on disk. It asks the backend nothing: a collision is about where
// sessions WILL be filed, so it is visible before a single session has run.

// ProjectFolder is one wick project as the collision check sees it.
type ProjectFolder struct {
	// ID is the wick project uuid, used to tell two same-named projects apart.
	ID string
	// Name is the project's display name, for the finding's wording.
	Name string
	// Folder is the directory a session in this project runs in — the
	// directory whose basename ai-memory would otherwise use.
	Folder string
	// CustomPath is true when the folder is the user's own. wick does not
	// write a marker into one, so these are the projects at risk.
	CustomPath bool
}

// Collision is a set of wick projects that resolve to ONE ai-memory project.
type Collision struct {
	// Workspace and Project are the single scope they all land in.
	Workspace string `json:"workspace"`
	Project   string `json:"project"`
	// Members are the wick projects sharing it, by display name.
	Members []CollisionMember `json:"members"`
	// Fixable is true when writing a marker would separate them — which is
	// the case for every collision wick can see, and is said explicitly so
	// the finding carries its own repair.
	Fixable bool `json:"fixable"`
}

// CollisionMember is one project caught in a collision.
type CollisionMember struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Folder string `json:"folder"`
	// HasMarker reports whether THIS folder pins its own scope — not whether
	// a marker governs it. The distinction decides the repair: a folder
	// governed by a parent's marker is separated by writing its own, while a
	// folder that already has one and still collides means two markers name
	// the same scope on purpose.
	HasMarker bool `json:"has_marker"`
}

// DetectCollisions groups projects by the ai-memory scope they actually
// resolve to and returns every group holding more than one.
//
// Resolution order matches the backend's: the folder's own marker wins, and a
// folder without one falls back to its basename in the default workspace. A
// folder that cannot be read is skipped rather than guessed at — reporting a
// collision that does not exist would send someone renaming a live project.
func DetectCollisions(workspace string, projects []ProjectFolder) []Collision {
	groups := map[Scope][]CollisionMember{}
	for _, p := range projects {
		folder := strings.TrimSpace(p.Folder)
		if folder == "" {
			continue
		}
		sc, marked := resolveScope(workspace, folder)
		if sc.Project == "" {
			continue
		}
		groups[sc] = append(groups[sc], CollisionMember{ID: p.ID, Name: p.Name, Folder: folder, HasMarker: marked})
	}

	var out []Collision
	for sc, members := range groups {
		if len(members) < 2 {
			continue
		}
		sort.Slice(members, func(i, j int) bool { return members[i].Folder < members[j].Folder })
		// Fixable when at least one member has no marker: writing one
		// separates it. When every member is marked, two markers agree on
		// purpose — a human wrote them, and this is a report, not a bug.
		fixable := false
		for _, m := range members {
			if !m.HasMarker {
				fixable = true
				break
			}
		}
		out = append(out, Collision{Workspace: sc.Workspace, Project: sc.Project, Members: members, Fixable: fixable})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out
}

// resolveScope works out where a folder's sessions land, and whether the
// folder pins that itself.
//
// The marker search walks UP, because that is what the backend's hook does:
// the closest marker wins, and a parent's marker governs every folder under
// it. A check that only looked in the folder itself would miss two sibling
// checkouts sharing one marked parent — which is a collision, not a
// coincidence.
//
// The second return is OWN, not FOUND. A folder governed by its parent has no
// marker of its own, and writing one is exactly the repair the finding should
// offer.
func resolveScope(workspace, folder string) (Scope, bool) {
	if sc, own, ok := readMarkerUpwards(folder); ok {
		if sc.Workspace == "" {
			sc.Workspace = defaultWorkspace(workspace)
		}
		return sc, own
	}
	return Scope{Workspace: defaultWorkspace(workspace), Project: filepath.Base(folder)}, false
}

// defaultWorkspace is what an unmarked folder lands in. The backend's own
// fallback is "default"; wick's marker names the install, so only the unmarked
// path reaches here.
func defaultWorkspace(ws string) string {
	if s := sanitizeName(ws); s != "" {
		return s
	}
	return "default"
}

// markerSearchDepth bounds the walk up from a project folder. Deep enough to
// cross a checkout and its parent, short of walking to the filesystem root on
// a host where nothing is marked at all.
const markerSearchDepth = 8

// readMarkerUpwards returns the governing scope, whether it is the folder's
// OWN marker, and whether one was found at all.
func readMarkerUpwards(folder string) (sc Scope, own, found bool) {
	dir := folder
	for i := 0; i < markerSearchDepth; i++ {
		if s, ok := readMarkerFile(filepath.Join(dir, MarkerName)); ok {
			return s, i == 0, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Scope{}, false, false
		}
		dir = parent
	}
	return Scope{}, false, false
}

// readMarkerFile pulls the workspace/project out of one marker file. Only ROOT
// keys count, for the same reason EnsureMarker only writes root keys: a
// `project` inside [capture] is a different setting entirely.
func readMarkerFile(path string) (Scope, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Scope{}, false
	}
	var sc Scope
	for _, l := range splitLines(string(b)) {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			break
		}
		if strings.HasPrefix(t, "#") {
			continue
		}
		name, val, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		v := strings.Trim(strings.TrimSpace(val), `"'`)
		if v == "" {
			continue
		}
		switch strings.TrimSpace(name) {
		case "workspace":
			sc.Workspace = v
		case "project":
			sc.Project = v
		}
	}
	return sc, sc.Project != ""
}

// projectLister supplies wick's own project folders. A var, injected by the
// hosting package, for the same reason loadInstances is: this package must not
// import the project registry, and the check has to be testable without one.
var projectLister func() []ProjectFolder

// SetProjectLister wires the source of wick's project folders. Called once at
// boot. Unwired means the collision check reports nothing rather than claiming
// there are none — see CollisionCheck.
func SetProjectLister(fn func() []ProjectFolder) { projectLister = fn }

// markerWorkspace is the workspace wick's own markers use. Set alongside the
// lister so the check resolves an unmarked folder into the same workspace a
// marked one would land in.
var markerWorkspace string

// SetMarkerWorkspace records the install's workspace name.
func SetMarkerWorkspace(ws string) { markerWorkspace = ws }

// CollisionCheck is the Health tab's payload for this check.
//
// Checked false is not "no collisions". It means wick could not enumerate its
// projects, and saying so is the whole difference between a clean bill of
// health and an unanswered question.
type CollisionCheck struct {
	Checked    bool        `json:"checked"`
	Reason     string      `json:"reason,omitempty"`
	Scanned    int         `json:"scanned"`
	Collisions []Collision `json:"collisions,omitempty"`
}

// RunCollisionCheck resolves wick's projects and reports the overlaps.
func RunCollisionCheck() CollisionCheck {
	if projectLister == nil {
		return CollisionCheck{Reason: "wick could not read its own project list, so nothing was compared."}
	}
	projects := projectLister()
	return CollisionCheck{
		Checked:    true,
		Scanned:    len(projects),
		Collisions: DetectCollisions(markerWorkspace, projects),
	}
}
