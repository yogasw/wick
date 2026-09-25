package agentmemory

import (
	"context"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/tool"
)

// Handlers for the Wiki and Handoffs tabs, and for the retention sweep the
// Settings tab drives. Split from handlers.go so the daemon-control core stays
// readable; the routes are registered alongside the rest in RegisterRoutes.

// defaultMessageLimit matches the backend's own mailbox listing default.
const defaultMessageLimit = 50

// registerWikiRoutes wires the 4C endpoints for one backend under the same
// prefix as the rest.
func registerWikiRoutes(r tool.Router, be *Backend, p string) {
	r.GET(p+"/page", view(be, pageHandler))
	r.GET(p+"/messages", view(be, messagesHandler))
	// Retiring a baton and sweeping the store both change it, so both sit
	// on the admin side of the split (PLAN §23.2).
	r.POST(p+"/handoffs/cancel", manage(be, cancelHandoffHandler))
	r.POST(p+"/forget-sweep", manage(be, forgetSweepHandler))
}

// pageHandler reads one wiki page in full — the other half of search, which
// only ever returns a snippet.
func pageHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	rd, ok := src.(PageReader)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": be.Desc.DisplayName + " cannot read a page"})
		return
	}
	path := strings.TrimSpace(c.Query("path"))
	if path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	scope, err := scopeFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), statusTimeout)
	defer cancel()

	pg, err := rd.ReadPage(ctx, connFor(be, set), scope, path)
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"page": pg})
}

// messagesHandler lists one project's cross-project mailbox.
func messagesHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	ml, ok := src.(MessageLister)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": be.Desc.DisplayName + " has no cross-project mailbox"})
		return
	}
	scope, err := scopeFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Anything that is not "outbox" reads the inbox. The two are different
	// questions ("what was sent to this project" vs "what it sent"), and a
	// typo must land on the read-only default rather than on the other one.
	box := "inbox"
	if strings.EqualFold(strings.TrimSpace(c.Query("box")), "outbox") {
		box = "outbox"
	}
	ctx, cancel := context.WithTimeout(c.Context(), statusTimeout)
	defer cancel()

	rows, err := ml.Messages(ctx, connFor(be, set), scope, box, formInt(c, "limit", defaultMessageLimit))
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	if rows == nil {
		rows = []Message{}
	}
	c.JSON(http.StatusOK, map[string]any{"messages": rows, "box": box})
}

// cancelHandoffWarning is the consequence the confirmation has to state.
// Cancelling is not "tidying a list": a handoff is a baton another agent may
// be about to pick up, and there is no undo — the next session in that project
// starts without the context the last one left behind.
const cancelHandoffWarning = "Cancelling retires the baton for good. Whatever the last session wrote down for the next one — the summary, the open questions, the next steps — stops being handed over, and any agent waiting on it gets nothing. It cannot be restored."

// cancelHandoffHandler retires ONE handoff by id.
//
// Note what is NOT here: the backend's `--expire-all`, which discards every
// open handoff in one call. It is deliberately not exposed anywhere in the UI
// (PLAN §20.2) — a single click that silently drops batons other agents are
// waiting on has no safe confirmation text.
func cancelHandoffHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	hc, ok := src.(HandoffCanceller)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": be.Desc.DisplayName + " cannot cancel a handoff"})
		return
	}
	id := strings.TrimSpace(c.Form("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "id is required"})
		return
	}
	if !formBool(c, "confirm", false) {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error":   "cancelling a handoff needs confirm=true",
			"warning": cancelHandoffWarning,
		})
		return
	}
	scope, err := scopeFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Context()), statusTimeout)
	defer cancel()

	res, err := hc.CancelHandoff(ctx, connFor(be, set), scope, id)
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	// res.Cancelled false is a successful call reporting that there was
	// nothing left to cancel. It is passed through untouched so the FE can
	// say "already gone" — folding it into an ok would tell the operator they
	// stopped something that had already stopped itself.
	c.JSON(http.StatusOK, map[string]any{"result": res, "warning": cancelHandoffWarning})
}

// sweepWarning is what a real sweep costs. Two different irreversible things
// happen under one button, and the second is the one that surprises people:
// evicting a cold page is recoverable until the hard-delete age, while pruning
// raw observations is not recoverable at all — the session can never be
// re-consolidated, not by a better model and not by a fixed bug, so its
// summary page becomes the only account of it that will ever exist.
const sweepWarning = "A sweep evicts pages whose retention score has decayed below the cold threshold, and — when a retention age is set — permanently deletes raw observations older than it. Pruned observations cannot be restored and the sessions they belong to can never be re-consolidated: the summary page becomes the only surviving record. Run the preview first."

// forgetSweepHandler runs the retention sweep. Like a forced backfill and a
// compaction, the real run refuses to proceed without confirm=true in the same
// request, so the fence lives with the action rather than only in the FE.
func forgetSweepHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	sw, ok := src.(Sweeper)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": be.Desc.DisplayName + " has no retention sweep"})
		return
	}
	dryRun := formBool(c, "dry_run", true)
	if !dryRun && !formBool(c, "confirm", false) {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error":   "a real sweep needs confirm=true",
			"warning": sweepWarning,
		})
		return
	}
	scope, err := scopeFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Context()), compactTimeout)
	defer cancel()

	rep, err := sw.ForgetSweep(ctx, connFor(be, set), scope, dryRun)
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"report": rep, "warning": sweepWarning})
}
