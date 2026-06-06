package agents

import (
	"net/http"
	"strconv"

	"github.com/yogasw/wick/internal/agents/workflow/repository"
	"github.com/yogasw/wick/pkg/tool"
)

// JSON endpoints for the workflow version history surface. Tables live
// alongside the file store during the migration window (see
// internal/docs/workflow/svelte-migration.md). When `globalDB` is nil
// (tests without a DB) the handlers return an empty result rather than
// erroring — keeps the SPA tab rendering clean.

func registerSPAWorkflowHistory(r tool.Router) {
	r.GET("/api/workflows/versions/{id}", spaWorkflowVersions)
	r.GET("/api/workflows/versions/{id}/diff", spaWorkflowVersionDiff)
	r.GET("/api/workflows/versions/{id}/{versionID}", spaWorkflowVersionDetail)
	r.POST("/api/workflows/versions/{id}/{versionID}/restore", spaWorkflowVersionRestore)
	r.DELETE("/api/workflows/versions/{id}/{versionID}", spaWorkflowVersionDelete)
	r.DELETE("/api/workflows/versions/{id}", spaWorkflowVersionsClear)
}

func newWorkflowRepo() *repository.Repo {
	if globalDB == nil {
		return nil
	}
	return repository.New(globalDB)
}

func spaWorkflowVersions(c *tool.Ctx) {
	repo := newWorkflowRepo()
	if repo == nil {
		c.JSON(http.StatusOK, map[string]any{"versions": []any{}})
		return
	}
	id := c.PathValue("id")
	versions, err := repo.Versions(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"versions": versions})
}

func spaWorkflowVersionDetail(c *tool.Ctx) {
	repo := newWorkflowRepo()
	if repo == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "DB not wired"})
		return
	}
	vidStr := c.PathValue("versionID")
	vid, err := strconv.ParseUint(vidStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid versionID"})
		return
	}
	v, err := repo.Version(uint(vid))
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	expected := c.PathValue("id")
	if v.WorkflowID != expected {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "version does not belong to this workflow"})
		return
	}
	c.JSON(http.StatusOK, v)
}

func spaWorkflowVersionRestore(c *tool.Ctx) {
	repo := newWorkflowRepo()
	if repo == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "DB not wired"})
		return
	}
	id := c.PathValue("id")
	vidStr := c.PathValue("versionID")
	vid, err := strconv.ParseUint(vidStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid versionID"})
		return
	}
	newDraftID, err := repo.Restore(id, uint(vid), "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "new_draft_version_id": newDraftID})
}

func spaWorkflowVersionDelete(c *tool.Ctx) {
	repo := newWorkflowRepo()
	if repo == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "DB not wired"})
		return
	}
	id := c.PathValue("id")
	vid, err := strconv.ParseUint(c.PathValue("versionID"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid versionID"})
		return
	}
	if err := repo.DeleteVersion(id, uint(vid)); err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func spaWorkflowVersionsClear(c *tool.Ctx) {
	repo := newWorkflowRepo()
	if repo == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "DB not wired"})
		return
	}
	id := c.PathValue("id")
	deleted, err := repo.ClearVersions(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
}

// spaWorkflowVersionDiff returns both snapshots' full body so the FE
// can render a side-by-side diff. Query string carries the version
// ids: ?from=<id>&to=<id>. Both must belong to the same workflow.
func spaWorkflowVersionDiff(c *tool.Ctx) {
	repo := newWorkflowRepo()
	if repo == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "DB not wired"})
		return
	}
	id := c.PathValue("id")
	from, err := strconv.ParseUint(c.Query("from"), 10, 32)
	if err != nil || from == 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "from query param is required (numeric version id)"})
		return
	}
	to, err := strconv.ParseUint(c.Query("to"), 10, 32)
	if err != nil || to == 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "to query param is required (numeric version id)"})
		return
	}
	fromRow, err := repo.Version(uint(from))
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "from: " + err.Error()})
		return
	}
	toRow, err := repo.Version(uint(to))
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "to: " + err.Error()})
		return
	}
	if fromRow.WorkflowID != id || toRow.WorkflowID != id {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "one or both versions do not belong to this workflow"})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"from": fromRow, "to": toRow})
}
