package agents

import (
	"net/http"

	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/pkg/tool"
)

// JSON endpoints powering the Svelte editor's bottom-panel tabs
// (Validation / Guard / Tests). Each one re-uses the existing
// workflow.Manager subsystems so the bottom tabs match the legacy
// editor's output byte-for-byte.

func registerSPAPanels(r tool.Router) {
	r.GET("/api/workflows/validate/{id}", spaWorkflowValidate)
	r.GET("/api/workflows/guard/{id}", spaWorkflowGuard)
	r.GET("/api/workflows/tests/{id}", spaWorkflowTestsList)
}

// spaWorkflowValidate runs the static validator against the draft.
// Returns the same `{ok, issues[]}` shape the legacy editor consumes
// via the inline validationJSON payload.
func spaWorkflowValidate(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	w, err := globalWorkflowMgr.Service.LoadDraft(id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	report := parse.Validate(w)
	c.JSON(http.StatusOK, validationPayload(report))
}

// spaWorkflowGuard returns the guard engine's review for the draft.
// Hits include rule id + node + severity + message so the FE can
// render them in a uniform list.
func spaWorkflowGuard(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	w, err := globalWorkflowMgr.Service.LoadDraft(id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	report := globalWorkflowMgr.Guard.Review(c.Context(), w)
	hits := []map[string]any{}
	for _, v := range report.Violations {
		hits = append(hits, map[string]any{
			"rule":     v.Rule,
			"node":     v.Node,
			"severity": v.Severity,
			"message":  v.Message,
		})
	}
	c.JSON(http.StatusOK, map[string]any{
		"hits":         hits,
		"ok":           report.OK,
		"content_hash": report.ContentHash,
	})
}

// spaWorkflowTestsList returns the `__tests__/*.json` fixtures for
// the workflow. Mirrors the legacy templ TestManager output but
// reshaped as JSON for the Svelte panel.
func spaWorkflowTestsList(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	items, err := loadTestCaseItems(id)
	if err != nil {
		c.JSON(http.StatusOK, map[string]any{"cases": []any{}})
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"name":       it.Name,
			"assertions": len(it.Case.Assertions),
		})
	}
	c.JSON(http.StatusOK, map[string]any{"cases": out})
}
