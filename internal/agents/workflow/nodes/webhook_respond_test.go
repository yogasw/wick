package nodes

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func TestWebhookRespondExecutor_BasicOutput(t *testing.T) {
	e := NewWebhookRespondExecutor()
	rc := &workflow.RunContext{
		Workflow: workflow.Workflow{ID: "wf1"},
		Event:    workflow.Event{Type: "webhook"},
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	n := workflow.Node{
		ID:          "respond_ok",
		Type:        workflow.NodeWebhookRespond,
		RespondStatus:  201,
		RespondBody:    `{"ok":true}`,
		RespondHeaders: map[string]string{"Content-Type": "application/json"},
	}
	out, err := e.Execute(context.Background(), n, rc)
	require.NoError(t, err)
	assert.Equal(t, 201, out.Fields["status"])
	assert.Equal(t, `{"ok":true}`, out.Fields["body"])
	hdrs, _ := out.Fields["headers"].(map[string]string)
	assert.Equal(t, "application/json", hdrs["Content-Type"])
}

func TestWebhookRespondExecutor_DefaultStatus200(t *testing.T) {
	e := NewWebhookRespondExecutor()
	rc := &workflow.RunContext{
		Workflow:    workflow.Workflow{ID: "wf1"},
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	n := workflow.Node{
		ID:          "respond_default",
		Type:        workflow.NodeWebhookRespond,
		RespondBody: "hello",
	}
	out, err := e.Execute(context.Background(), n, rc)
	require.NoError(t, err)
	assert.Equal(t, 200, out.Fields["status"])
	assert.Equal(t, "hello", out.Fields["body"])
}

func TestWebhookRespondExecutor_TemplateBody(t *testing.T) {
	e := NewWebhookRespondExecutor()
	rc := &workflow.RunContext{
		Workflow: workflow.Workflow{ID: "wf1"},
		NodeOutputs: map[string]workflow.NodeOutput{
			"fetch": {Fields: map[string]any{"id": "abc123"}},
		},
	}
	n := workflow.Node{
		ID:          "respond_tmpl",
		Type:        workflow.NodeWebhookRespond,
		RespondBody: `{"id":"{{.Node.fetch.id}}"}`,
	}
	out, err := e.Execute(context.Background(), n, rc)
	require.NoError(t, err)
	assert.Equal(t, `{"id":"abc123"}`, out.Fields["body"])
}

func TestWebhookRespondExecutor_Descriptor(t *testing.T) {
	e := NewWebhookRespondExecutor()
	d := e.Descriptor()
	assert.NotEmpty(t, d.Description)
	assert.NotEmpty(t, d.Schema)
	assert.Contains(t, d.Output, "status")
	assert.Contains(t, d.Output, "body")
	assert.Contains(t, d.Output, "headers")
}
