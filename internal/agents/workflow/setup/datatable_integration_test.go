// Integration test for datatable_* nodes: dedup webhook flow + multi-step
// shared table. Runs through Manager → Engine.Run so the executor is
// reached via the real engine dispatch, not unit-style. Reserved trio
// (id/created_at/updated_at) is appended by CreateTable; tests dedup on
// a user-defined natural key column.
package setup

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/datatable"
)

// seed registers a strict schema before the workflow runs.
func seedTable(t *testing.T, m *Manager, sc datatable.Schema) {
	t.Helper()
	require.NoError(t, m.DataTables.(*datatable.MockService).CreateTable(sc))
}

func TestDataTableIntegration_DedupWebhook(t *testing.T) {
	m := newMgr(t)
	require.NoError(t, m.Start(context.Background()))

	// `event_id` is the natural key the workflow dedups against; the
	// auto-incremented int `id` is the table's primary key.
	seedTable(t, m, datatable.Schema{
		Slug: "events",
		Columns: []datatable.Column{
			{Name: "event_id", Type: "string", Required: true},
			{Name: "status", Type: "enum", Enum: []string{"received", "done"}},
		},
	})

	w := workflow.Workflow{
		ID:      "uc-dedup",
		Version: 1,
		Name:    "Dedup webhook",
		Enabled: true,
		Triggers: []workflow.Trigger{
			{Type: workflow.TriggerManual, Label: "Run"},
		},
		Graph: workflow.Graph{
			Entry: "check",
			Nodes: []workflow.Node{
				{
					ID: "check", Type: workflow.NodeDataTableExists, Table: "events",
					Where: map[string]any{"event_id": `{{index .Event.Payload "id"}}`},
				},
				{ID: "skip", Type: workflow.NodeEnd, Result: "already_done"},
				{
					ID: "mark", Type: workflow.NodeDataTableInsert, Table: "events",
					RowValues: map[string]any{
						"event_id": `{{index .Event.Payload "id"}}`,
						"status":   "received",
					},
				},
				{ID: "ok", Type: workflow.NodeEnd, Result: "processed"},
			},
			Edges: []workflow.Edge{
				{From: "check", To: "skip", Case: "true"},
				{From: "check", To: "mark", Case: "false"},
				{From: "mark", To: "ok"},
			},
		},
	}

	// First run: should follow mark → ok and persist row.
	id1 := runWorkflow(t, m, w, workflow.Event{
		Type: string(workflow.TriggerManual), At: time.Now(),
		Payload: map[string]any{"id": "evt-1"},
	})
	st1, err := m.StateStore.Load(w.ID, id1)
	require.NoError(t, err)
	require.Equal(t, workflow.StatusSuccess, st1.Status)
	require.Contains(t, st1.Completed, "mark", "first run must insert")
	require.NotContains(t, st1.Completed, "skip", "first run must not skip")

	// Second run with same natural key: drive Engine directly so we
	// don't re-Create the workflow (name collision otherwise).
	loaded, err := m.Service.Load(w.ID)
	require.NoError(t, err)
	st2, err := m.Engine.Run(context.Background(), loaded, workflow.Event{
		Type: string(workflow.TriggerManual), At: time.Now(),
		Payload: map[string]any{"id": "evt-1"},
	})
	require.NoError(t, err)
	require.Equal(t, workflow.StatusSuccess, st2.Status)
	require.Contains(t, st2.Completed, "skip", "duplicate must skip")
	require.NotContains(t, st2.Completed, "mark", "duplicate must not double-insert")

	// Service confirms a single row.
	count, err := m.DataTables.Count("events", map[string]any{"event_id": "evt-1"})
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestDataTableIntegration_InsertAndQuery(t *testing.T) {
	m := newMgr(t)
	require.NoError(t, m.Start(context.Background()))

	seedTable(t, m, datatable.Schema{
		Slug: "tickets",
		Columns: []datatable.Column{
			{Name: "code", Type: "string", Required: true},
			{Name: "priority", Type: "int"},
			{Name: "status", Type: "enum", Enum: []string{"open", "closed"}},
		},
	})

	// 3 inserts → query priority gte 5 desc.
	w := workflow.Workflow{
		ID:      "uc-insert-query",
		Version: 1,
		Name:    "Insert + query",
		Enabled: true,
		Triggers: []workflow.Trigger{
			{Type: workflow.TriggerManual, Label: "Run"},
		},
		Graph: workflow.Graph{
			Entry: "i1",
			Nodes: []workflow.Node{
				{
					ID: "i1", Type: workflow.NodeDataTableInsert, Table: "tickets",
					RowValues: map[string]any{"code": "T1", "priority": 1, "status": "open"},
				},
				{
					ID: "i2", Type: workflow.NodeDataTableInsert, Table: "tickets",
					RowValues: map[string]any{"code": "T2", "priority": 5, "status": "open"},
				},
				{
					ID: "i3", Type: workflow.NodeDataTableInsert, Table: "tickets",
					RowValues: map[string]any{"code": "T3", "priority": 9, "status": "closed"},
				},
				{
					ID: "q", Type: workflow.NodeDataTableQuery, Table: "tickets",
					Conditions: []workflow.DataTableCondYAML{
						{Column: "priority", Op: "gte", Value: 5},
					},
					OrderBy: []workflow.DataTableOrder{{Column: "priority", Direction: "desc"}},
				},
				{ID: "done", Type: workflow.NodeEnd, Result: "ok"},
			},
			Edges: []workflow.Edge{
				{From: "i1", To: "i2"},
				{From: "i2", To: "i3"},
				{From: "i3", To: "q"},
				{From: "q", To: "done"},
			},
		},
	}

	runID := runWorkflow(t, m, w, workflow.Event{Type: string(workflow.TriggerManual), At: time.Now()})
	st, err := m.StateStore.Load(w.ID, runID)
	require.NoError(t, err)
	require.Equal(t, workflow.StatusSuccess, st.Status)

	// q output should hold T3 then T2 (priority desc, gte 5).
	qOut, ok := st.Outputs["q"].(map[string]any)
	require.True(t, ok, "q output missing: %v", st.Outputs)
	switch rows := qOut["rows"].(type) {
	case []map[string]any:
		require.Len(t, rows, 2)
		require.Equal(t, "T3", rows[0]["code"])
		require.Equal(t, "T2", rows[1]["code"])
	case []any:
		require.Len(t, rows, 2)
		r0 := rows[0].(map[string]any)
		r1 := rows[1].(map[string]any)
		require.Equal(t, "T3", r0["code"])
		require.Equal(t, "T2", r1["code"])
	default:
		t.Fatalf("unexpected rows shape %T: %v", qOut["rows"], qOut["rows"])
	}
}
