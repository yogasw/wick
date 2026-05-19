package nodes

import (
	"context"
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/dataset"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/template"
)

type datasetGetSchema struct {
	Dataset string `wick:"required;key=dataset;desc=Dataset name"`
	Key     string `wick:"required;key=key;desc=Primary key value (template expression)"`
}

type datasetUpsertSchema struct {
	Dataset   string `wick:"required;key=dataset"`
	Key       string `wick:"required;key=key;desc=Primary key value"`
	RowValues string `wick:"required;key=row;desc=YAML map of fields to write"`
}

type datasetQuerySchema struct {
	Dataset string `wick:"required;key=dataset"`
	Where   string `wick:"key=where;desc=YAML map of field equality filters"`
	OrderBy string `wick:"key=order_by"`
	Limit   int    `wick:"key=limit"`
	Offset  int    `wick:"key=offset"`
}

type datasetWhereSchema struct {
	Dataset string `wick:"required;key=dataset"`
	Where   string `wick:"required;key=where;desc=YAML map of field equality filters"`
}

type datasetCountSchema struct {
	Dataset string `wick:"required;key=dataset"`
	Where   string `wick:"key=where"`
}

// DatasetDescriptor returns the descriptor for one dataset_* node type.
// Used by setup/manager.go RegisterWithDesc since one executor handles
// all 7 dataset types.
func DatasetDescriptor(t workflow.NodeType) engine.NodeDescriptor {
	switch t {
	case workflow.NodeDatasetGet:
		return engine.NodeDescriptor{
			Description: "Load one row by primary key. Branches on found/not_found.",
			WhenToUse:   "Lookup a state row before deciding next action.",
			Schema:      integration.StructSchema(datasetGetSchema{}),
			Output:      map[string]string{"row": "map[string]any — loaded row (nil if not_found branch taken)"},
		}
	case workflow.NodeDatasetExists:
		return engine.NodeDescriptor{
			Description: "Check whether any row matches. Branches on true/false.",
			WhenToUse:   "Dedup webhook events or guard against duplicate work.",
			Schema:      integration.StructSchema(datasetWhereSchema{}),
		}
	case workflow.NodeDatasetQuery:
		return engine.NodeDescriptor{
			Description: "Multi-row search with where/order_by/limit.",
			WhenToUse:   "List or paginate stored rows.",
			Schema:      integration.StructSchema(datasetQuerySchema{}),
			Output:      map[string]string{"rows": "[]map[string]any — matched rows", "count": "int"},
		}
	case workflow.NodeDatasetCount:
		return engine.NodeDescriptor{
			Description: "Count rows matching where without loading them.",
			WhenToUse:   "Cheap statistic for decisions.",
			Schema:      integration.StructSchema(datasetCountSchema{}),
			Output:      map[string]string{"count": "int — matching row count"},
		}
	case workflow.NodeDatasetInsert:
		return engine.NodeDescriptor{
			Description: "Insert a new row; fails on PK conflict.",
			WhenToUse:   "Idempotency-by-PK guard plus persistence.",
			Schema:      integration.StructSchema(datasetUpsertSchema{}),
			Output:      map[string]string{"key": "string — inserted primary key"},
		}
	case workflow.NodeDatasetUpsert:
		return engine.NodeDescriptor{
			Description: "Insert or update by primary key. Returns action: insert|update.",
			WhenToUse:   "Idempotent record sync.",
			Schema:      integration.StructSchema(datasetUpsertSchema{}),
			Output:      map[string]string{"action": "string — insert|update"},
		}
	case workflow.NodeDatasetDelete:
		return engine.NodeDescriptor{
			Description: "Delete rows matching where.",
			WhenToUse:   "Cleanup expired state.",
			Schema:      integration.StructSchema(datasetWhereSchema{}),
		}
	}
	return engine.NodeDescriptor{}
}

// DatasetExecutor handles all 7 dataset_* node types.
type DatasetExecutor struct {
	Service dataset.Service
}

// NewDatasetExecutor wires the executor.
func NewDatasetExecutor(svc dataset.Service) *DatasetExecutor {
	return &DatasetExecutor{Service: svc}
}

// Execute dispatches per node.Type.
func (e *DatasetExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	if e.Service == nil {
		return workflow.NodeOutput{}, fmt.Errorf("dataset: no service configured")
	}
	if n.Dataset == "" {
		return workflow.NodeOutput{}, fmt.Errorf("dataset: node %q missing dataset field", n.ID)
	}
	if err := e.enforceAccess(n, rc); err != nil {
		return workflow.NodeOutput{}, err
	}
	rctx := rc.RenderCtx()
	where, err := renderMap(n.Where, rctx)
	if err != nil {
		return workflow.NodeOutput{}, err
	}
	key, err := renderMap(n.Key, rctx)
	if err != nil {
		return workflow.NodeOutput{}, err
	}
	row, err := renderMap(n.RowValues, rctx)
	if err != nil {
		return workflow.NodeOutput{}, err
	}

	switch n.Type {
	case workflow.NodeDatasetExists:
		found, err := e.Service.Exists(n.Dataset, where)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Verdict: boolStr(found), Fields: map[string]any{"found": found}}, nil

	case workflow.NodeDatasetGet:
		got, found, err := e.Service.Get(n.Dataset, key)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		verdict := "not_found"
		if found {
			verdict = "found"
		}
		return workflow.NodeOutput{Verdict: verdict, Fields: map[string]any{"found": found, "row": got}}, nil

	case workflow.NodeDatasetQuery:
		rows, err := e.Service.Query(n.Dataset, where, n.OrderBy, n.Limit, n.Offset)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Result: rows, Fields: map[string]any{"rows": rows, "row_count": len(rows)}}, nil

	case workflow.NodeDatasetCount:
		count, err := e.Service.Count(n.Dataset, where)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Result: count, Fields: map[string]any{"count": count}}, nil

	case workflow.NodeDatasetInsert:
		if err := e.Service.Insert(n.Dataset, row); err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Fields: map[string]any{"success": true, "row": row}}, nil

	case workflow.NodeDatasetUpsert:
		action, err := e.Service.Upsert(n.Dataset, row)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Fields: map[string]any{"action": action, "row": row}}, nil

	case workflow.NodeDatasetDelete:
		count, err := e.Service.Delete(n.Dataset, where)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Fields: map[string]any{"deleted_count": count}}, nil
	}
	return workflow.NodeOutput{}, fmt.Errorf("dataset: unsupported node type %q", n.Type)
}

func (e *DatasetExecutor) enforceAccess(n workflow.Node, rc *workflow.RunContext) error {
	sc, err := e.Service.LoadSchema(n.Dataset)
	if err != nil {
		return err
	}
	if len(sc.Access.Workflows) > 0 && isWriteOp(n.Type) {
		if !containsStr(sc.Access.Workflows, rc.Workflow.ID) {
			return fmt.Errorf("dataset %q: workflow %q not in access.workflows", n.Dataset, rc.Workflow.ID)
		}
	}
	return nil
}

func isWriteOp(t workflow.NodeType) bool {
	switch t {
	case workflow.NodeDatasetInsert, workflow.NodeDatasetUpsert, workflow.NodeDatasetDelete:
		return true
	}
	return false
}

func renderMap(in map[string]any, rctx workflow.RenderCtx) (map[string]any, error) {
	if in == nil {
		return nil, nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok {
			rs, err := template.Render(s, rctx)
			if err != nil {
				return nil, fmt.Errorf("render %q: %w", k, err)
			}
			out[k] = rs
			continue
		}
		out[k] = v
	}
	return out, nil
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
