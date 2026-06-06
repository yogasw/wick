package setup

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func minimalWorkflow(id, name string) workflow.Workflow {
	return workflow.Workflow{
		ID:      id,
		Version: 1,
		Name:    name,
		Enabled: true,
		Triggers: []workflow.Trigger{
			{Type: workflow.TriggerManual, Label: "run"},
		},
		Graph: workflow.Graph{
			Entry: "end",
			Nodes: []workflow.Node{
				{ID: "end", Type: workflow.NodeEnd, Result: "ok"},
			},
		},
	}
}

func TestPublishAndReload_RefreshesRouterDefinition(t *testing.T) {
	m := newMgr(t)
	require.NoError(t, m.Start(context.Background()))
	ctx := context.Background()
	id := "pub-reload"

	require.NoError(t, m.Service.Create(id, minimalWorkflow(id, "v1")))
	require.NoError(t, HotReload(ctx, m.Service, m.Router, m.Cron, m.ScheduleAt, id))

	got, ok := m.Router.Definition(id)
	require.True(t, ok)
	require.Equal(t, "v1", got.Name)

	require.NoError(t, m.Service.SaveDraft(id, minimalWorkflow(id, "v2")))

	pub, err := PublishAndReload(ctx, m.Service, m.Router, m.Cron, m.ScheduleAt, id)
	require.NoError(t, err)
	require.Equal(t, "v2", pub.Name)

	got2, ok := m.Router.Definition(id)
	require.True(t, ok)
	require.Equal(t, "v2", got2.Name, "router must serve the freshly published body after PublishAndReload")
}

func TestManagerMCPReload_RefreshesRouterDefinition(t *testing.T) {
	m := newMgr(t)
	require.NoError(t, m.Start(context.Background()))
	require.NotNil(t, m.MCP.Reload, "Manager.New must wire MCP.Reload so MCP/connector publish refreshes the router")
	ctx := context.Background()
	id := "mcp-reload"

	require.NoError(t, m.Service.Create(id, minimalWorkflow(id, "v1")))
	require.NoError(t, HotReload(ctx, m.Service, m.Router, m.Cron, m.ScheduleAt, id))
	got, ok := m.Router.Definition(id)
	require.True(t, ok)
	require.Equal(t, "v1", got.Name)

	require.NoError(t, m.Service.SaveDraft(id, minimalWorkflow(id, "v2")))
	if _, err := m.Service.Publish(id); err != nil {
		t.Fatalf("publish: %v", err)
	}
	require.NoError(t, m.MCP.Reload(id))

	got2, ok := m.Router.Definition(id)
	require.True(t, ok)
	require.Equal(t, "v2", got2.Name)
}
