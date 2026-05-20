package nodes

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/datatable"
)

// newDTExec builds a wired executor + service. The reserved trio
// (id/created_at/updated_at) is appended by CreateTable automatically.
func newDTExec(t *testing.T) (*DataTableExecutor, *datatable.MockService) {
	t.Helper()
	svc := datatable.NewMock()
	if err := svc.CreateTable(datatable.Schema{
		Slug: "events",
		Columns: []datatable.Column{
			{Name: "key", Type: "string", Required: true},
			{Name: "status", Type: "enum", Enum: []string{"open", "done"}},
			{Name: "priority", Type: "int"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return NewDataTableExecutor(svc), svc
}

func runDT(t *testing.T, ex *DataTableExecutor, n workflow.Node, payload map[string]any) workflow.NodeOutput {
	t.Helper()
	rc := &workflow.RunContext{
		Workflow:    workflow.Workflow{ID: "wf-test"},
		Event:       workflow.Event{Type: "manual", Payload: payload},
		EnvValues:   map[string]string{},
		Secrets:     map[string]string{},
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	out, err := ex.Execute(context.Background(), n, rc)
	if err != nil {
		t.Fatalf("execute %s: %v", n.Type, err)
	}
	return out
}

func TestDT_InsertExistsGet(t *testing.T) {
	ex, _ := newDTExec(t)
	runDT(t, ex, workflow.Node{
		ID: "ins", Type: workflow.NodeDataTableInsert, Table: "events",
		RowValues: map[string]any{"key": "k1", "status": "open", "priority": 5},
	}, nil)
	out := runDT(t, ex, workflow.Node{
		ID: "ex", Type: workflow.NodeDataTableExists, Table: "events",
		Where: map[string]any{"key": "k1"},
	}, nil)
	if out.Verdict != "true" {
		t.Fatalf("exists verdict = %q want true", out.Verdict)
	}
	out = runDT(t, ex, workflow.Node{
		ID: "g", Type: workflow.NodeDataTableGet, Table: "events",
		Key: map[string]any{"key": "k1"},
	}, nil)
	if out.Verdict != "found" {
		t.Fatalf("get verdict = %q want found", out.Verdict)
	}
	row, _ := out.Fields["row"].(map[string]any)
	if row["status"] != "open" {
		t.Fatalf("loaded row wrong: %v", row)
	}
}

func TestDT_GetNotFound(t *testing.T) {
	ex, _ := newDTExec(t)
	out := runDT(t, ex, workflow.Node{
		ID: "g", Type: workflow.NodeDataTableGet, Table: "events",
		Key: map[string]any{"key": "nope"},
	}, nil)
	if out.Verdict != "not_found" {
		t.Fatalf("verdict = %q want not_found", out.Verdict)
	}
}

func TestDT_UpsertInsertThenUpdateByID(t *testing.T) {
	ex, _ := newDTExec(t)
	out := runDT(t, ex, workflow.Node{
		ID: "u", Type: workflow.NodeDataTableUpsert, Table: "events",
		RowValues: map[string]any{"key": "k", "status": "open"},
	}, nil)
	if out.Fields["action"] != "insert" {
		t.Fatalf("first upsert action = %v want insert", out.Fields["action"])
	}
	// Second upsert targets the autoassigned id=1 to drive an update.
	out = runDT(t, ex, workflow.Node{
		ID: "u2", Type: workflow.NodeDataTableUpsert, Table: "events",
		RowValues: map[string]any{"id": int64(1), "key": "k", "status": "done"},
	}, nil)
	if out.Fields["action"] != "update" {
		t.Fatalf("second upsert action = %v want update", out.Fields["action"])
	}
}

func TestDT_QueryWithWhereAndOrder(t *testing.T) {
	ex, _ := newDTExec(t)
	for i, v := range []map[string]any{
		{"key": "a", "status": "open", "priority": 3},
		{"key": "b", "status": "open", "priority": 7},
		{"key": "c", "status": "done", "priority": 1},
	} {
		runDT(t, ex, workflow.Node{
			ID: "i" + itoaShort(i), Type: workflow.NodeDataTableInsert, Table: "events",
			RowValues: v,
		}, nil)
	}
	out := runDT(t, ex, workflow.Node{
		ID: "q", Type: workflow.NodeDataTableQuery, Table: "events",
		Where:   map[string]any{"status": "open"},
		OrderBy: []workflow.DataTableOrder{{Column: "priority", Direction: "desc"}},
	}, nil)
	rows, _ := out.Fields["rows"].([]map[string]any)
	if len(rows) != 2 || rows[0]["key"] != "b" {
		t.Fatalf("query rows wrong: %v", rows)
	}
}

func TestDT_CountAndDeleteByCondition(t *testing.T) {
	ex, _ := newDTExec(t)
	for _, v := range []map[string]any{
		{"key": "a", "status": "open", "priority": 1},
		{"key": "b", "status": "open", "priority": 9},
		{"key": "c", "status": "done", "priority": 5},
	} {
		runDT(t, ex, workflow.Node{
			ID: "i", Type: workflow.NodeDataTableInsert, Table: "events", RowValues: v,
		}, nil)
	}
	out := runDT(t, ex, workflow.Node{
		ID: "co", Type: workflow.NodeDataTableCount, Table: "events",
		Conditions: []workflow.DataTableCondYAML{{Column: "priority", Op: "gt", Value: 4}},
	}, nil)
	if out.Fields["count"] != 2 {
		t.Fatalf("count = %v want 2", out.Fields["count"])
	}
	del := runDT(t, ex, workflow.Node{
		ID: "del", Type: workflow.NodeDataTableDelete, Table: "events",
		Conditions: []workflow.DataTableCondYAML{{Column: "status", Op: "equals", Value: "open"}},
	}, nil)
	if del.Fields["deleted_count"] != 2 {
		t.Fatalf("deleted_count = %v want 2", del.Fields["deleted_count"])
	}
}

func TestDT_AccessAllowlistEnforced(t *testing.T) {
	svc := datatable.NewMock()
	if err := svc.CreateTable(datatable.Schema{
		Slug: "events",
		Columns: []datatable.Column{
			{Name: "key", Type: "string"},
		},
		Access: datatable.Access{Workflows: []string{"allowed-wf"}},
	}); err != nil {
		t.Fatal(err)
	}
	ex := NewDataTableExecutor(svc)
	rc := &workflow.RunContext{
		Workflow:    workflow.Workflow{ID: "other-wf"},
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	_, err := ex.Execute(context.Background(), workflow.Node{
		ID: "i", Type: workflow.NodeDataTableInsert, Table: "events",
		RowValues: map[string]any{"key": "k"},
	}, rc)
	if err == nil {
		t.Fatal("expected access deny")
	}
	rc.Workflow.ID = "allowed-wf"
	_, err = ex.Execute(context.Background(), workflow.Node{
		ID: "i", Type: workflow.NodeDataTableInsert, Table: "events",
		RowValues: map[string]any{"key": "k"},
	}, rc)
	if err != nil {
		t.Fatalf("allowed-wf insert: %v", err)
	}
}

func TestDT_TemplateRenderInWhere(t *testing.T) {
	ex, _ := newDTExec(t)
	runDT(t, ex, workflow.Node{
		ID: "i", Type: workflow.NodeDataTableInsert, Table: "events",
		RowValues: map[string]any{"key": "abc", "status": "open"},
	}, nil)
	out := runDT(t, ex, workflow.Node{
		ID: "g", Type: workflow.NodeDataTableGet, Table: "events",
		Key: map[string]any{"key": `{{index .Event.Payload "want"}}`},
	}, map[string]any{"want": "abc"})
	if out.Verdict != "found" {
		t.Fatalf("templated key resolution failed: %v", out)
	}
}

func TestDT_MissingTableField(t *testing.T) {
	ex, _ := newDTExec(t)
	rc := &workflow.RunContext{Workflow: workflow.Workflow{ID: "wf"}, NodeOutputs: map[string]workflow.NodeOutput{}}
	_, err := ex.Execute(context.Background(), workflow.Node{
		ID: "i", Type: workflow.NodeDataTableInsert,
	}, rc)
	if err == nil {
		t.Fatal("expected missing table error")
	}
}

func itoaShort(i int) string {
	switch i {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	}
	return "n"
}
