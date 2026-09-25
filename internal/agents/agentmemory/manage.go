package agentmemory

import (
	"context"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/tool"
)

// Editing one project's memory (PLAN §22 — the reason the project menu entry
// exists at all).
//
// Reading what an agent remembered is half the question; the other half is
// "that is wrong, fix it". Until now the panel could only watch. These four
// calls are what make the memory MANAGEABLE: write a page, delete a page,
// and — because the wiki is a git tree — list the checkpoints and restore a
// page from one.
//
// The restore half is not a nicety. People are correctly nervous about
// editing what an agent will later recall as fact, and the honest answer to
// that nervousness is not a confirmation dialog, it is a way back. Every
// write the backend makes is committed, so every edit here is recoverable,
// and the UI says so where the edit happens.
//
// All four are optional interfaces, the same pattern the package already
// uses for pages, mailboxes and compaction: a backend that cannot write says
// so by not implementing them and gets a 501 that names the limitation,
// rather than failing somewhere deeper with a message about a missing flag.

// PageWriter writes or updates one wiki page.
type PageWriter interface {
	// WritePage writes body at path within the scope, creating the page or
	// replacing it. The backend indexes it in the same operation, so a page
	// written here is immediately searchable — and committed, so it is
	// immediately restorable.
	WritePage(ctx context.Context, conn Conn, s ReadScope, w PageWrite) (*PageWriteResult, error)
}

// PageDeleter removes one wiki page.
type PageDeleter interface {
	DeletePage(ctx context.Context, conn Conn, s ReadScope, path string) (*PageDeleteResult, error)
}

// CheckpointLister lists the wiki's git checkpoints — the history an edit can
// be undone from.
type CheckpointLister interface {
	Checkpoints(ctx context.Context, conn Conn, limit int) ([]Checkpoint, error)
}

// PageRestorer puts one page back as it was at a checkpoint.
type PageRestorer interface {
	RestorePage(ctx context.Context, conn Conn, s ReadScope, path, from string) (*RestoreResult, error)
}

// PageWrite is one edit. Title and Kind are optional: the backend derives a
// title from the body's first heading when it is empty, and an empty kind
// leaves whatever the page already carries.
type PageWrite struct {
	Path  string
	Body  string
	Title string
	Kind  string
}

// PageWriteResult is what a write reports back. Output is the backend's own
// line ("✓ wrote notes/foo.md (page_id=01a0d8dd) under wick/demo") — carried
// verbatim because it names the scope the write actually landed in, which is
// the one thing worth double-checking after an edit.
type PageWriteResult struct {
	Path   string `json:"path"`
	PageID string `json:"page_id,omitempty"`
	Output string `json:"output,omitempty"`
}

// PageDeleteResult names what was removed. Path is echoed rather than assumed
// from the request: a delete that resolved to a different scope is the
// failure this whole feature has to make visible.
type PageDeleteResult struct {
	Path    string `json:"path"`
	Deleted bool   `json:"deleted"`
	Output  string `json:"output,omitempty"`
}

// Checkpoint is one commit in the wiki's git history.
type Checkpoint struct {
	OID      string `json:"oid"`
	ShortOID string `json:"short_oid"`
	// TimeUnix is seconds, as git reports them.
	TimeUnix int64 `json:"time"`
	// Summary is the commit subject — "write-page wick/demo: notes/foo.md",
	// "session 01a0c33c: …" — which is what makes a checkpoint list
	// choosable rather than a wall of hashes.
	Summary string `json:"summary"`
}

// RestoreResult is one restore's outcome, including the checkpoint the
// restore ITSELF created: undoing an undo is the next thing someone wants.
type RestoreResult struct {
	PageID        string `json:"page_id,omitempty"`
	Path          string `json:"path"`
	RestoredFrom  string `json:"restored_from,omitempty"`
	PreCheckpoint string `json:"pre_checkpoint,omitempty"`
	Checkpoint    string `json:"checkpoint,omitempty"`
}

// maxPageBytes caps one edit. Wiki pages are consolidated notes measured in
// kilobytes; a megabyte arriving here is a mistake or a paste accident, and
// the store is the one place a mistake is expensive to undo.
const maxPageBytes = 1 << 20

// defaultCheckpointLimit is how much history the restore picker shows. Twenty
// is the backend's own default and about a day of activity on a busy project.
const defaultCheckpointLimit = 20

// registerManageRoutes wires the editing endpoints. Every one is on the
// MANAGE side of the §23 split: a viewer reads this project's memory and
// never changes it.
func registerManageRoutes(r tool.Router, be *Backend, p string) {
	r.POST(p+"/page/write", manage(be, writePageHandler))
	r.POST(p+"/page/delete", manage(be, deletePageHandler))
	r.GET(p+"/checkpoints", manage(be, checkpointsHandler))
	r.POST(p+"/page/restore", manage(be, restorePageHandler))
}

// writePageHandler saves one page.
func writePageHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	w, ok := src.(PageWriter)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": be.Desc.DisplayName + " cannot write a page"})
		return
	}
	scope, err := scopeFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// The scope is required for a WRITE even though a read can fall back:
	// the backend resolves an unnamed scope from a working directory, and a
	// write that lands in the wrong project is not a wrong answer, it is a
	// wrong fact stored in someone else's memory (PLAN §14).
	if scope.Workspace == "" || scope.Project == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "a write needs an explicit workspace and project"})
		return
	}
	path := strings.TrimSpace(c.Form("path"))
	if path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	body := c.Form("body")
	if strings.TrimSpace(body) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "an empty page is a delete — use the delete button, which says what it removes"})
		return
	}
	if len(body) > maxPageBytes {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "this page is larger than the 1 MiB limit for an edit"})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Context()), statusTimeout)
	defer cancel()

	res, err := w.WritePage(ctx, connFor(be, set), scope, PageWrite{
		Path:  path,
		Body:  body,
		Title: strings.TrimSpace(c.Form("title")),
		Kind:  strings.TrimSpace(c.Form("kind")),
	})
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"result": res, "note": editsAreCommitted})
}

// editsAreCommitted is the sentence the UI shows next to an edit. It is here
// rather than in the FE because it is a fact about the BACKEND's behaviour —
// every write is committed to the wiki's git tree — and the FE should not be
// the thing asserting it.
const editsAreCommitted = "Every write is committed to the wiki's git history, so this edit can be restored from a checkpoint."

// deletePageHandler removes one page, and answers with what it removed.
func deletePageHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	d, ok := src.(PageDeleter)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": be.Desc.DisplayName + " cannot delete a page"})
		return
	}
	scope, err := scopeFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if scope.Workspace == "" || scope.Project == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "a delete needs an explicit workspace and project"})
		return
	}
	path := strings.TrimSpace(c.Form("path"))
	if path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	// Deleting is the one action here that removes something, so it carries
	// the same confirm=true fence as a forced backfill and a compaction:
	// the refusal lives with the action rather than only in whichever FE
	// happens to call it.
	if !formBool(c, "confirm", false) {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error":   "deleting a page needs confirm=true",
			"warning": deleteWarning,
		})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Context()), statusTimeout)
	defer cancel()

	res, err := d.DeletePage(ctx, connFor(be, set), scope, path)
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"result": res, "warning": deleteWarning})
}

// deleteWarning states the consequence, and the way back. Both halves matter:
// the fact that an agent stops recalling this is the cost, and the fact that
// the page is still in git is what makes the cost survivable.
const deleteWarning = "The page stops being recalled immediately. It stays in the wiki's git history, so it can be restored from a checkpoint."

// checkpointsHandler lists the wiki's recent commits.
//
// It is on the MANAGE side with the write endpoints, not with the reads: a
// checkpoint list is a history of every project's writes in one store, and it
// is only useful next to a restore button.
func checkpointsHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	l, ok := src.(CheckpointLister)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": be.Desc.DisplayName + " keeps no checkpoints"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), statusTimeout)
	defer cancel()

	rows, err := l.Checkpoints(ctx, connFor(be, set), formInt(c, "limit", defaultCheckpointLimit))
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	if rows == nil {
		rows = []Checkpoint{}
	}
	c.JSON(http.StatusOK, map[string]any{"checkpoints": rows})
}

// restorePageHandler puts one page back as it was at a checkpoint.
func restorePageHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	r, ok := src.(PageRestorer)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": be.Desc.DisplayName + " cannot restore a page"})
		return
	}
	scope, err := scopeFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if scope.Workspace == "" || scope.Project == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "a restore needs an explicit workspace and project"})
		return
	}
	path := strings.TrimSpace(c.Form("path"))
	from := strings.TrimSpace(c.Form("from"))
	if path == "" || from == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path and from are required"})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Context()), statusTimeout)
	defer cancel()

	res, err := r.RestorePage(ctx, connFor(be, set), scope, path, from)
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	// The restore made its own checkpoint. Saying so is what turns "I undid
	// it" into "and I can undo that too".
	c.JSON(http.StatusOK, map[string]any{"result": res, "note": restoreNote})
}

const restoreNote = "The restore is itself a checkpoint, so the version you just replaced is still recoverable."
