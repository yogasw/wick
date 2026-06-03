package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow/parse"
	wftest "github.com/yogasw/wick/internal/agents/workflow/wftest"
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
	r.GET("/api/workflows/tests/{id}/{name}", spaWorkflowTestGet)
	r.POST("/api/workflows/tests/{id}", spaWorkflowTestSave)
	r.POST("/api/workflows/tests/{id}/{name}/run", spaWorkflowTestRun)
	r.POST("/api/workflows/tests/{id}/{name}/delete", spaWorkflowTestDelete)
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

// spaWorkflowTestGet returns one case fixture (input + assertions)
// so the editor can pre-fill the modal.
func spaWorkflowTestGet(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	name := c.PathValue("name")
	data, err := globalWorkflowMgr.MCP.GetTest(id, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]any{"error": "test case not found"})
		return
	}
	var tc wftest.Case
	if err := json.Unmarshal(data, &tc); err != nil {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid test case JSON"})
		return
	}
	if tc.Name == "" {
		tc.Name = name
	}
	c.JSON(http.StatusOK, tc)
}

// spaWorkflowTestSave creates or replaces a case fixture. Name is
// validated slug-safe (matches v1 saveTestCase).
func spaWorkflowTestSave(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	var body struct {
		Name       string             `json:"name"`
		Input      wftest.Input       `json:"input"`
		Assertions []wftest.Assertion `json:"assertions"`
	}
	if err := json.NewDecoder(c.R.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	}
	for _, ch := range body.Name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			c.JSON(http.StatusBadRequest, map[string]any{"error": "name must be slug-safe (a-z, 0-9, dash, underscore)"})
			return
		}
	}
	tc := wftest.Case{
		Name:       body.Name,
		Input:      body.Input,
		Assertions: body.Assertions,
	}
	data, err := json.MarshalIndent(tc, "", "  ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if err := globalWorkflowMgr.MCP.SaveTest(id, body.Name, data); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "name": body.Name})
}

// spaWorkflowTestRun executes a single case and returns the JSON
// Result so the FE can render pass/fail + failures inline.
func spaWorkflowTestRun(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	name := c.PathValue("name")
	data, err := globalWorkflowMgr.MCP.GetTest(id, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]any{"error": "test case not found"})
		return
	}
	var tc wftest.Case
	if err := json.Unmarshal(data, &tc); err != nil {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid test case JSON"})
		return
	}
	w, err := globalWorkflowMgr.Service.Load(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	runner := wftest.New(globalWorkflowMgr.Engine, globalWorkflowMgr.Service, globalWorkflowMgr.Layout)
	result := runner.RunOne(context.Background(), w, tc)
	c.JSON(http.StatusOK, map[string]any{
		"name":        result.Name,
		"pass":        result.Pass,
		"failures":    result.Failures,
		"node_output": result.NodeOutput,
		"duration_ms": result.Duration.Milliseconds(),
	})
}

// spaWorkflowTestDelete removes a case fixture.
func spaWorkflowTestDelete(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	name := c.PathValue("name")
	if err := globalWorkflowMgr.MCP.DeleteTest(id, name); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}
