package agents

// context_stat.go — does a file a trace names still exist?
//
// A tool call in the trace (a Read of a screenshot, a Write of a report)
// names a path that may since have been deleted or overwritten. The trace
// card asks here, lazily and in one batch, before it offers to open it.

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/pkg/tool"
)

// maxStatPaths caps one batch; a trace card asks for one or two.
const maxStatPaths = 50

// fileStat is one path's answer. Status is "present", "missing", or
// "unknown" — the last for anything outside the session folder or that
// could not be stat'ed, so the reply never says whether such a path exists.
type fileStat struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	// Rel is the path relative to the session folder, usable with
	// /files/raw. Set only when present.
	Rel   string `json:"rel,omitempty"`
	Size  int64  `json:"size,omitempty"`
	MTime int64  `json:"mtime,omitempty"` // unix ms
}

// statTracePath resolves p (absolute, or relative to cwd) with the same
// guard the file endpoints use and stats it.
func statTracePath(cwd, p string) fileStat {
	out := fileStat{Path: p, Status: "unknown"}
	rel := p
	if filepath.IsAbs(p) {
		rel = ""
		for _, base := range cwdForms(cwd) {
			if r, err := filepath.Rel(base, filepath.Clean(p)); err == nil && r != ".." && !strings.HasPrefix(r, "../") {
				rel = r
				break
			}
		}
		if rel == "" {
			return out
		}
	}
	full, err := safeJoin(cwd, rel)
	if err != nil {
		return out
	}
	info, err := os.Stat(full)
	if errors.Is(err, fs.ErrNotExist) {
		out.Status = "missing"
		return out
	}
	if err != nil || info.IsDir() {
		return out
	}
	out.Status, out.Rel, out.Size, out.MTime = "present", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixMilli()
	return out
}

// cwdForms is cwd as written and with symlinks resolved: a trace records
// whichever form the agent's shell used.
func cwdForms(cwd string) []string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	forms := []string{abs}
	if r, err := filepath.EvalSymlinks(abs); err == nil && r != abs {
		forms = append(forms, r)
	}
	return forms
}

// sessionContextStat answers GET /sessions/{id}/files/stat?path=a&path=b.
func sessionContextStat(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	paths := c.R.URL.Query()["path"]
	if len(paths) == 0 {
		c.Error(http.StatusBadRequest, "path required")
		return
	}
	if len(paths) > maxStatPaths {
		paths = paths[:maxStatPaths]
	}
	sess, ok := globalMgr.Registry().Session(c.PathValue("id"))
	if !ok || !ownsSession(c, sess) {
		c.Error(http.StatusNotFound, "session not found")
		return
	}
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	files := make([]fileStat, 0, len(paths))
	for _, p := range paths {
		files = append(files, statTracePath(cwd, p))
	}
	c.JSON(http.StatusOK, map[string]any{"files": files})
}
