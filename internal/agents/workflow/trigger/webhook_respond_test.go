package trigger

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// TestWriteWebhookResult_LastNode verifies last_node mode returns
// the last completed node's output as JSON.
func TestWriteWebhookResult_LastNode(t *testing.T) {
	done := make(chan RunResult, 1)
	done <- RunResult{
		State: workflow.RunState{
			Status:    workflow.StatusSuccess,
			Completed: []string{"step1", "step2"},
			Outputs: map[string]any{
				"step1": map[string]any{"result": "first"},
				"step2": map[string]any{"result": "last"},
			},
		},
	}
	w := httptest.NewRecorder()
	writeWebhookResult(w, done, workflow.RespondModeLastNode)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "last")
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
}

// TestWriteWebhookResult_RespondNode verifies respond_node mode uses
// the webhook_respond node's status/body/headers.
func TestWriteWebhookResult_RespondNode(t *testing.T) {
	done := make(chan RunResult, 1)
	done <- RunResult{
		State: workflow.RunState{
			Status:    workflow.StatusSuccess,
			Completed: []string{"respond_ok"},
			Outputs: map[string]any{
				"respond_ok": map[string]any{
					"_webhook_respond": true,
					"status":           201,
					"body":             `{"created":true}`,
					"headers":          map[string]any{"Content-Type": "application/json"},
				},
			},
		},
	}
	w := httptest.NewRecorder()
	writeWebhookResult(w, done, workflow.RespondModeRespondNode)
	assert.Equal(t, 201, w.Code)
	assert.Equal(t, `{"created":true}`, w.Body.String())
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
}

// TestWriteWebhookResult_Timeout verifies 504 when run doesn't complete.
func TestWriteWebhookResult_Timeout(t *testing.T) {
	orig := respondTimeout
	respondTimeout = 50 * time.Millisecond
	defer func() { respondTimeout = orig }()

	done := make(chan RunResult) // never sends
	w := httptest.NewRecorder()
	writeWebhookResult(w, done, workflow.RespondModeLastNode)
	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
}

// TestExtractRespondNodeOutput covers the output extraction helper.
// Sentinel key "_webhook_respond" must be present.
func TestExtractRespondNodeOutput(t *testing.T) {
	st := workflow.RunState{
		Completed: []string{"n1"},
		Outputs: map[string]any{
			"n1": map[string]any{
				"_webhook_respond": true,
				"status":           200,
				"body":             "ok",
				"headers":          map[string]any{"X-Custom": "yes"},
			},
		},
	}
	status, body, hdrs, found := extractRespondNodeOutput(st)
	assert.True(t, found)
	assert.Equal(t, 200, status)
	assert.Equal(t, "ok", body)
	assert.Equal(t, "yes", hdrs["X-Custom"])
}

// TestExtractRespondNodeOutput_NoRespond — http node output lacks sentinel → not found.
func TestExtractRespondNodeOutput_NoRespond(t *testing.T) {
	st := workflow.RunState{
		Completed: []string{"http_1"},
		Outputs: map[string]any{
			"http_1": map[string]any{"status": 200, "body": "raw", "headers": map[string]any{}},
		},
	}
	_, _, _, found := extractRespondNodeOutput(st)
	assert.False(t, found)
}

// TestRespondModeFor verifies the router looks up respond_mode correctly.
func TestRespondModeFor(t *testing.T) {
	r := newTestRouter()
	wf := workflow.Workflow{
		ID:      "wf-respond",
		Enabled: true,
		Triggers: []workflow.Trigger{
			{
				ID:          "t1",
				Type:        workflow.TriggerWebhook,
				Path:        "orders",
				RespondMode: workflow.RespondModeLastNode,
			},
		},
	}
	r.mu.Lock()
	r.defs[wf.ID] = wf
	r.mu.Unlock()

	mode := r.respondModeFor("/wf-respond/orders", workflow.Event{})
	require.Equal(t, workflow.RespondModeLastNode, mode)

	// Unknown path → empty (immediately by default)
	mode = r.respondModeFor("/wf-respond/unknown", workflow.Event{})
	assert.Equal(t, "", mode)
}
