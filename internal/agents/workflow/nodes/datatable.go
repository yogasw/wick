package nodes

import (
	"context"
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/datatable"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/template"
)

type datatableGetSchema struct {
	Table string `wick:"required;key=table;desc=Data table alias from the workflow's data_tables: block"`
	Key   string `wick:"required;key=key;desc=Primary key value (template expression)"`
}

type datatableUpsertSchema struct {
	Table     string `wick:"required;key=table"`
	Key       string `wick:"required;key=key;desc=Primary key value"`
	RowValues string `wick:"required;key=row;desc=YAML map of fields to write"`
}

type datatableQuerySchema struct {
	Table      string `wick:"required;key=table"`
	Where      string `wick:"key=where;desc=YAML map of field equality filters"`
	Conditions string `wick:"key=conditions;desc=YAML list of {column, op, value} predicates (n8n parity: equals|not_equals|gt|gte|lt|lte|contains|in|is_empty|is_not_empty)"`
	OrderBy    string `wick:"key=order_by"`
	Limit      int    `wick:"key=limit"`
	Offset     int    `wick:"key=offset"`
}

type datatableWhereSchema struct {
	Table      string `wick:"required;key=table"`
	Where      string `wick:"key=where;desc=YAML map of field equality filters"`
	Conditions string `wick:"key=conditions;desc=YAML list of {column, op, value} predicates"`
}

type datatableCountSchema struct {
	Table      string `wick:"required;key=table"`
	Where      string `wick:"key=where"`
	Conditions string `wick:"key=conditions"`
}

// DataTableDescriptor returns the descriptor for one datatable_* node type.
// Used by setup/manager.go RegisterWithDesc since one executor handles
// all 7 datatable types.
func DataTableDescriptor(t workflow.NodeType) engine.NodeDescriptor {
	switch t {
	case workflow.NodeDataTableGet:
		return engine.NodeDescriptor{
			Description: "Load one row by primary key. Branches on found/not_found.",
			WhenToUse:   "Lookup a state row before deciding next action.",
			Schema:      integration.StructSchema(datatableGetSchema{}),
			Output:      map[string]string{"row": "map[string]any — loaded row (nil if not_found branch taken)"},
		}
	case workflow.NodeDataTableExists:
		return engine.NodeDescriptor{
			Description: "Check whether any row matches. Branches on true/false.",
			WhenToUse:   "Dedup webhook events or guard against duplicate work.",
			Schema:      integration.StructSchema(datatableWhereSchema{}),
		}
	case workflow.NodeDataTableQuery:
		return engine.NodeDescriptor{
			Description: "Multi-row search with where/order_by/limit.",
			WhenToUse:   "List or paginate stored rows.",
			Schema:      integration.StructSchema(datatableQuerySchema{}),
			Output:      map[string]string{"rows": "[]map[string]any — matched rows", "count": "int"},
		}
	case workflow.NodeDataTableCount:
		return engine.NodeDescriptor{
			Description: "Count rows matching where without loading them.",
			WhenToUse:   "Cheap statistic for decisions.",
			Schema:      integration.StructSchema(datatableCountSchema{}),
			Output:      map[string]string{"count": "int — matching row count"},
		}
	case workflow.NodeDataTableInsert:
		return engine.NodeDescriptor{
			Description: "Insert a new row; fails on PK conflict.",
			WhenToUse:   "Idempotency-by-PK guard plus persistence.",
			Schema:      integration.StructSchema(datatableUpsertSchema{}),
			Output:      map[string]string{"key": "string — inserted primary key"},
		}
	case workflow.NodeDataTableUpsert:
		return engine.NodeDescriptor{
			Description: "Insert or update by primary key. Returns action: insert|update.",
			WhenToUse:   "Idempotent record sync.",
			Schema:      integration.StructSchema(datatableUpsertSchema{}),
			Output:      map[string]string{"action": "string — insert|update"},
		}
	case workflow.NodeDataTableDelete:
		return engine.NodeDescriptor{
			Description: "Delete rows matching where.",
			WhenToUse:   "Cleanup expired state.",
			Schema:      integration.StructSchema(datatableWhereSchema{}),
		}
	}
	return engine.NodeDescriptor{}
}

// DataTableExecutor handles all 7 datatable_* node types.
type DataTableExecutor struct {
	Service datatable.Service
}

// NewDataTableExecutor wires the executor.
func NewDataTableExecutor(svc datatable.Service) *DataTableExecutor {
	return &DataTableExecutor{Service: svc}
}

// Execute dispatches per node.Type.
func (e *DataTableExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	if e.Service == nil {
		return workflow.NodeOutput{}, fmt.Errorf("datatable: no service configured")
	}
	if n.Table == "" {
		return workflow.NodeOutput{}, fmt.Errorf("datatable: node %q missing table field", n.ID)
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
	conds, err := renderConditions(n.Conditions, rctx)
	if err != nil {
		return workflow.NodeOutput{}, err
	}
	useConds := len(conds) > 0

	switch n.Type {
	case workflow.NodeDataTableExists:
		if useConds {
			c, err := e.Service.CountConditions(n.Table, conds)
			if err != nil {
				return workflow.NodeOutput{}, err
			}
			return workflow.NodeOutput{Verdict: boolStr(c > 0), Fields: map[string]any{"found": c > 0}}, nil
		}
		found, err := e.Service.Exists(n.Table, where)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Verdict: boolStr(found), Fields: map[string]any{"found": found}}, nil

	case workflow.NodeDataTableGet:
		got, found, err := e.Service.Get(n.Table, key)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		verdict := "not_found"
		if found {
			verdict = "found"
		}
		return workflow.NodeOutput{Verdict: verdict, Fields: map[string]any{"found": found, "row": got}}, nil

	case workflow.NodeDataTableQuery:
		var rows []map[string]any
		if useConds {
			rows, err = e.Service.QueryConditions(n.Table, conds, n.OrderBy, n.Limit, n.Offset)
		} else {
			rows, err = e.Service.Query(n.Table, where, n.OrderBy, n.Limit, n.Offset)
		}
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Result: rows, Fields: map[string]any{"rows": rows, "row_count": len(rows), "count": len(rows)}}, nil

	case workflow.NodeDataTableCount:
		var count int
		if useConds {
			count, err = e.Service.CountConditions(n.Table, conds)
		} else {
			count, err = e.Service.Count(n.Table, where)
		}
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Result: count, Fields: map[string]any{"count": count}}, nil

	case workflow.NodeDataTableInsert:
		if err := e.Service.Insert(n.Table, row); err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Fields: map[string]any{"success": true, "row": row}}, nil

	case workflow.NodeDataTableUpsert:
		action, err := e.Service.Upsert(n.Table, row)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Fields: map[string]any{"action": action, "row": row}}, nil

	case workflow.NodeDataTableDelete:
		var count int
		if useConds {
			count, err = e.Service.DeleteConditions(n.Table, conds)
		} else {
			count, err = e.Service.Delete(n.Table, where)
		}
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Fields: map[string]any{"deleted_count": count}}, nil
	}
	return workflow.NodeOutput{}, fmt.Errorf("datatable: unsupported node type %q", n.Type)
}

func (e *DataTableExecutor) enforceAccess(n workflow.Node, rc *workflow.RunContext) error {
	sc, err := e.Service.LoadSchema(n.Table)
	if err != nil {
		// Schema not registered yet — allowed (autocreate on first Insert via SaveSchema in tests).
		return nil
	}
	if len(sc.Access.Workflows) > 0 && isWriteOp(n.Type) {
		if !containsStr(sc.Access.Workflows, rc.Workflow.ID) {
			return fmt.Errorf("data table %q: workflow %q not in access.workflows", n.Table, rc.Workflow.ID)
		}
	}
	return nil
}

func isWriteOp(t workflow.NodeType) bool {
	switch t {
	case workflow.NodeDataTableInsert, workflow.NodeDataTableUpsert, workflow.NodeDataTableDelete:
		return true
	}
	return false
}

// renderConditions resolves templated condition values against the
// render context. Strings get template-expanded; other types pass through.
func renderConditions(in []workflow.DataTableCondYAML, rctx workflow.RenderCtx) ([]datatable.Condition, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]datatable.Condition, 0, len(in))
	for _, c := range in {
		val := c.Value
		if s, ok := val.(string); ok {
			rs, err := template.Render(s, rctx)
			if err != nil {
				return nil, fmt.Errorf("render condition %q: %w", c.Column, err)
			}
			val = rs
		}
		out = append(out, datatable.Condition{Column: c.Column, Op: c.Op, Value: val})
	}
	return out, nil
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
