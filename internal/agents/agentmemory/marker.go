package agentmemory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MarkerName is the per-folder scope marker ai-memory looks for. Its hook
// walks up from the session's cwd to $HOME and uses the FIRST marker it finds,
// so the closest one wins.
const MarkerName = ".ai-memory.toml"

// Why this file exists at all: ai-memory derives a session's project from the
// BASENAME of its working directory. Every wick session runs in
// `projects/<uuid>/files`, whose basename is "files" — so without a marker
// EVERY project on a host, for every client, collapses into one ai-memory
// project called "files" and their memories pile up together (PLAN §14.4).
// The marker overrides the basename entirely, which is the fix that was
// actually tested: default/proj2 became qiscus/wick-8c28230d once it was
// written (PLAN §14.5).

const (
	// maxSlugLen caps the readable half of a project name so the id suffix is
	// never squeezed out of a long project title.
	maxSlugLen = 32
	// shortIDLen is how much of the wick project UUID is carried. 8 hex is the
	// form §14.5 tested with and is plenty to separate two projects that share
	// a display name.
	shortIDLen = 8
	// maxNameLen bounds a whole sanitised name.
	maxNameLen = 64
)

// Scope is the ai-memory workspace/project a folder resolves to. Both names
// are server-validated, so they only ever hold [a-z0-9-] — build one via
// ProjectScope rather than by hand.
type Scope struct {
	Workspace string
	Project   string
}

// ProjectScope maps a wick project onto an ai-memory scope.
//
// The mapping is deliberate:
//
//   - workspace = the wick installation (its app name). wick has no notion of
//     "client" to hang a workspace on; what it does have is one host per
//     install, and workspace is the only axis above project, so grouping by
//     install is the honest reading. An operator who wants a per-client
//     workspace edits the marker — that edit is preserved (see EnsureMarker).
//   - project = "<slug of the project name>-<first 8 of its uuid>". The slug
//     makes the bucket recognisable in ai-memory's own UI; the uuid suffix is
//     what actually guarantees two unrelated projects both called "backend"
//     never share a memory, which is the whole point of §14.3.
//
// A project with no usable uuid is an error rather than a fallback name: a
// shared fallback bucket would silently recreate the very collision this
// function exists to prevent.
func ProjectScope(workspace, projectName, projectID string) (Scope, error) {
	ws := sanitizeName(workspace)
	if ws == "" {
		return Scope{}, fmt.Errorf("agentmemory: workspace %q sanitises to nothing", workspace)
	}
	short := sanitizeName(projectID)
	if len(short) > shortIDLen {
		short = short[:shortIDLen]
	}
	if short == "" {
		return Scope{}, fmt.Errorf("agentmemory: project id %q sanitises to nothing", projectID)
	}
	slug := sanitizeName(projectName)
	if len(slug) > maxSlugLen {
		slug = strings.Trim(slug[:maxSlugLen], "-")
	}
	name := short
	if slug != "" {
		name = slug + "-" + short
	}
	return Scope{Workspace: ws, Project: name}, nil
}

// sanitizeName reduces s to the lowercase [a-z0-9-] alphabet the ai-memory
// server validates names against: anything else (spaces, slashes, emoji,
// accents) becomes a single dash, and leading/trailing dashes are dropped.
// Returns "" when nothing survives — callers must treat that as "no name",
// never as a usable value.
func sanitizeName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		default:
			// Collapse any run of junk into one dash, and never open with one.
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxNameLen {
		out = strings.Trim(out[:maxNameLen], "-")
	}
	return out
}

// EnsureMarker pins dir to sc by writing <dir>/.ai-memory.toml.
//
// It is additive, never authoritative: a key already present in the file keeps
// its value, and every other line — other keys, [capture] / [recall] sections,
// comments — is carried through byte for byte. So it is idempotent (a correct
// marker means no write at all), it cannot clobber a name a human pinned on
// purpose, and it cannot move a project's memory out from under it after a
// rename.
func EnsureMarker(dir string, sc Scope) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("agentmemory: marker dir is empty")
	}
	if sc.Workspace == "" || sc.Project == "" {
		return fmt.Errorf("agentmemory: incomplete scope %+v", sc)
	}

	path := filepath.Join(dir, MarkerName)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := splitLines(string(existing))

	want := [][2]string{{"workspace", sc.Workspace}, {"project", sc.Project}}
	var missing []string
	for _, kv := range want {
		if rootKeyPresent(lines, kv[0]) {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s = %q", kv[0], kv[1]))
	}
	if len(missing) == 0 {
		return nil
	}

	// Root keys must sit above the first [section] header — TOML binds any key
	// after a header to that table, so appending at the end would define
	// `[capture].project`, not the scope.
	at := len(lines)
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			at = i
			break
		}
	}
	out := append([]string{}, lines[:at]...)
	out = append(out, missing...)
	out = append(out, lines[at:]...)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

// rootKeyPresent reports whether key is assigned a non-empty value at the root
// of the document — above the first [section] header. A `project` inside
// [capture] is a different key and must not be mistaken for the scope.
func rootKeyPresent(lines []string, key string) bool {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			return false
		}
		if strings.HasPrefix(t, "#") {
			continue
		}
		name, val, ok := strings.Cut(t, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(val), `"'`) != ""
	}
	return false
}

// splitLines splits file content into lines, dropping the trailing empty
// element a final newline produces so a rewrite doesn't grow a blank line per
// pass.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// MarkerWriter writes the scope marker into a project folder. It satisfies the
// registry's project-marker hook, so the registry never imports this package.
//
// Nothing is written unless Enabled reports true: a host that doesn't use
// Agent Memory should not find wick's dotfiles in its project folders.
type MarkerWriter struct {
	// Workspace is the ai-memory workspace every project on this host lands
	// in — see ProjectScope for why that is the install's own name.
	Workspace string
	// Enabled gates every write. nil = never write.
	Enabled func() bool
}

// Ensure writes the marker for one wick project. Called on project creation
// and whenever a session moves into a project, which is also what backfills
// projects that existed before the feature was switched on.
func (w MarkerWriter) Ensure(folder, projectName, projectID string) error {
	if w.Enabled == nil || !w.Enabled() {
		return nil
	}
	sc, err := ProjectScope(w.Workspace, projectName, projectID)
	if err != nil {
		return err
	}
	return EnsureMarker(folder, sc)
}

// ScopeSource names how a project's memory bucket was decided, because the
// three answers carry different confidence and the panel says so.
type ScopeSource string

const (
	// ScopeFromMarker: a .ai-memory.toml governs the folder. This is what
	// the agent's own hook reads, so it is the bucket in use right now.
	ScopeFromMarker ScopeSource = "marker"
	// ScopeFromWick: no marker yet, but wick manages the folder and will
	// write one the next time a session moves in — so this is where the
	// memory is going to land.
	ScopeFromWick ScopeSource = "wick"
	// ScopeFromBasename: a custom-path folder wick deliberately does not
	// mark, so the backend derives the project from the folder name — the
	// one case that can collide with another project (see collision.go).
	ScopeFromBasename ScopeSource = "basename"
)

// ScopeForProjectID resolves one wick project id onto the memory bucket its
// sessions use, for the project-scoped entry into the panel (PLAN §22).
//
// It is deliberately NOT a bare ProjectScope call. The marker is what the
// agent's hook actually reads, so a folder that has one is answered from it —
// including a marker a human edited, which ProjectScope alone would
// contradict. Only an unmarked folder falls through to ProjectScope, and only
// when wick is the one that will mark it; a custom-path folder gets the
// basename the backend would derive, because that is the truth for it.
//
// The FE never computes any of this: two sources of truth for "which bucket"
// is the failure §22.2 exists to prevent.
func ScopeForProjectID(id string) (ProjectFolder, Scope, ScopeSource, bool) {
	id = strings.TrimSpace(id)
	if id == "" || projectLister == nil {
		return ProjectFolder{}, Scope{}, "", false
	}
	var p ProjectFolder
	found := false
	for _, cand := range projectLister() {
		if cand.ID == id {
			p, found = cand, true
			break
		}
	}
	if !found || strings.TrimSpace(p.Folder) == "" {
		return ProjectFolder{}, Scope{}, "", false
	}
	if sc, _, ok := readMarkerUpwards(p.Folder); ok {
		if sc.Workspace == "" {
			sc.Workspace = defaultWorkspace(markerWorkspace)
		}
		return p, sc, ScopeFromMarker, true
	}
	if !p.CustomPath {
		if sc, err := ProjectScope(markerWorkspace, p.Name, p.ID); err == nil {
			return p, sc, ScopeFromWick, true
		}
	}
	return p, Scope{Workspace: defaultWorkspace(markerWorkspace), Project: filepath.Base(p.Folder)}, ScopeFromBasename, true
}
