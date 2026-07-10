package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/datatable"
	wfmcp "github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/pkg/connector"
)

// DataTableACL gates the MCP/agent data-table ops (datatable_*) per caller.
// Wired at boot via SetDataTableACL; nil ⇒ no gating (pre-ownership
// behaviour). The workflow ENGINE does not reach these ops — it drives
// datatable.Service directly with its own Access.Workflows enforcement — so
// this scopes only the agent-facing tool surface. A call with no caller user
// id (internal/system context) always bypasses the gate.
type DataTableACL interface {
	// CanAccess reports whether userID may read/write the given table.
	CanAccess(ctx context.Context, userID, slug string) bool
	// RegisterOwner records userID as the owner of a table it just created.
	RegisterOwner(ctx context.Context, userID, slug string)
}

var dataTableACL DataTableACL

// SetDataTableACL wires the per-caller data-table gate. Called once at boot.
func SetDataTableACL(a DataTableACL) { dataTableACL = a }

// guardDataTable denies a caller access to a table they don't own or weren't
// granted. Returns a not-found error on denial so slugs don't leak. No-op
// when the ACL is unwired or the caller is an internal context (no user id).
func guardDataTable(c *connector.Ctx, slug string) error {
	uid := c.CallerUserID()
	if uid == "" || dataTableACL == nil {
		return nil
	}
	if !dataTableACL.CanAccess(c.Context(), uid, slug) {
		return fmt.Errorf("data table %q not found", slug)
	}
	return nil
}

// ── Data Tables MCP handlers ─────────────────────────────────────────

func (h *handlers) datatableList(c *connector.Ctx) (any, error) {
	out, err := h.ops.DataTableList()
	if err != nil {
		return nil, err
	}
	uid := c.CallerUserID()
	if uid == "" || dataTableACL == nil {
		return out, nil
	}
	filtered := out[:0]
	for _, s := range out {
		if dataTableACL.CanAccess(c.Context(), uid, s.Slug) {
			filtered = append(filtered, s)
		}
	}
	return filtered, nil
}

func (h *handlers) datatableGet(c *connector.Ctx) (any, error) {
	if err := guardDataTable(c, c.Input("slug")); err != nil {
		return nil, err
	}
	return h.ops.DataTableGet(c.Input("slug"))
}

func (h *handlers) datatableCreate(c *connector.Ctx) (any, error) {
	cols, err := parseColumnsForMCP(c.Input("columns"))
	if err != nil {
		return nil, err
	}
	in := wfmcp.DataTableCreateInput{
		Slug:    c.Input("slug"),
		Mode:    c.Input("mode"),
		Columns: cols,
	}
	if pk := strings.TrimSpace(c.Input("primary_key")); pk != "" {
		parts := strings.Split(pk, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				in.PrimaryKey = append(in.PrimaryKey, p)
			}
		}
	}
	if raw := strings.TrimSpace(c.Input("access")); raw != "" {
		var acc datatable.Access
		if err := json.Unmarshal([]byte(raw), &acc); err != nil {
			return nil, fmt.Errorf("parse access: %w", err)
		}
		in.Access = &acc
	}
	if err := h.ops.DataTableCreate(in); err != nil {
		return nil, err
	}
	// Record the caller as owner so the table is scoped to them (parity with
	// the Data Tables UI). No-op for internal callers / unwired ACL.
	if uid := c.CallerUserID(); uid != "" && dataTableACL != nil {
		dataTableACL.RegisterOwner(c.Context(), uid, in.Slug)
	}
	return map[string]any{"ok": true, "slug": in.Slug}, nil
}

func (h *handlers) datatableDrop(c *connector.Ctx) (any, error) {
	if err := guardDataTable(c, c.Input("slug")); err != nil {
		return nil, err
	}
	if err := h.ops.DataTableDrop(c.Input("slug")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (h *handlers) datatableQuery(c *connector.Ctx) (any, error) {
	if err := guardDataTable(c, c.Input("slug")); err != nil {
		return nil, err
	}
	in := wfmcp.DataTableQueryInput{Slug: c.Input("slug")}
	if raw := strings.TrimSpace(c.Input("where")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Where); err != nil {
			return nil, fmt.Errorf("parse where: %w", err)
		}
	}
	if raw := strings.TrimSpace(c.Input("conditions")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Conditions); err != nil {
			return nil, fmt.Errorf("parse conditions: %w", err)
		}
	}
	if raw := strings.TrimSpace(c.Input("order_by")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.OrderBy); err != nil {
			return nil, fmt.Errorf("parse order_by: %w", err)
		}
	}
	in.Limit = c.InputInt("limit")
	in.Offset = c.InputInt("offset")
	rows, err := h.ops.DataTableQuery(in)
	if err != nil {
		return nil, err
	}
	return map[string]any{"rows": rows, "count": len(rows)}, nil
}

func (h *handlers) datatableInsert(c *connector.Ctx) (any, error) {
	if err := guardDataTable(c, c.Input("slug")); err != nil {
		return nil, err
	}
	row, err := parseRowJSON(c.Input("row"))
	if err != nil {
		return nil, err
	}
	if err := h.ops.DataTableInsert(wfmcp.DataTableInsertInput{Slug: c.Input("slug"), Row: row}); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "row": row}, nil
}

func (h *handlers) datatableUpsert(c *connector.Ctx) (any, error) {
	if err := guardDataTable(c, c.Input("slug")); err != nil {
		return nil, err
	}
	row, err := parseRowJSON(c.Input("row"))
	if err != nil {
		return nil, err
	}
	action, err := h.ops.DataTableUpsert(wfmcp.DataTableInsertInput{Slug: c.Input("slug"), Row: row})
	if err != nil {
		return nil, err
	}
	return map[string]any{"action": action, "row": row}, nil
}

func (h *handlers) datatableDelete(c *connector.Ctx) (any, error) {
	if err := guardDataTable(c, c.Input("slug")); err != nil {
		return nil, err
	}
	in, err := parseFilterInput(c)
	if err != nil {
		return nil, err
	}
	n, err := h.ops.DataTableDelete(in)
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted_count": n}, nil
}

func (h *handlers) datatableCount(c *connector.Ctx) (any, error) {
	if err := guardDataTable(c, c.Input("slug")); err != nil {
		return nil, err
	}
	in, err := parseFilterInput(c)
	if err != nil {
		return nil, err
	}
	n, err := h.ops.DataTableCount(in)
	if err != nil {
		return nil, err
	}
	return map[string]any{"count": n}, nil
}

// ── helpers ──────────────────────────────────────────────────────────

func parseRowJSON(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("row is required")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse row: %w", err)
	}
	return out, nil
}

func parseFilterInput(c *connector.Ctx) (wfmcp.DataTableDeleteInput, error) {
	in := wfmcp.DataTableDeleteInput{Slug: c.Input("slug")}
	if raw := strings.TrimSpace(c.Input("where")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Where); err != nil {
			return in, fmt.Errorf("parse where: %w", err)
		}
	}
	if raw := strings.TrimSpace(c.Input("conditions")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &in.Conditions); err != nil {
			return in, fmt.Errorf("parse conditions: %w", err)
		}
	}
	return in, nil
}

// parseColumnsForMCP parses `name:type` per line into Column structs.
func parseColumnsForMCP(raw string) ([]datatable.Column, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("columns is required")
	}
	lines := strings.Split(raw, "\n")
	out := []datatable.Column{}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		parts := strings.SplitN(l, ":", 2)
		name := strings.TrimSpace(parts[0])
		typ := "string"
		if len(parts) == 2 {
			typ = strings.TrimSpace(parts[1])
		}
		if name == "" {
			continue
		}
		out = append(out, datatable.Column{Name: name, Type: typ})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one column required")
	}
	return out, nil
}

// Compile-time use of wf to satisfy import (DataTableCondition lives there).
var _ = wf.NodeDataTableGet
