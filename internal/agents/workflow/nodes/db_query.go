package nodes

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/template"

	// register drivers used by workflow db_query nodes
	_ "github.com/glebarez/go-sqlite"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type dbQuerySchema struct {
	Database string `wick:"required;key=database;desc=DSN reference key configured in workspace"`
	SQL      string `wick:"required;key=sql;textarea;desc=Parameterized SQL query"`
	SQLArgs  string `wick:"key=sql_args;desc=YAML list of positional args for ? placeholders"`
}

func (e *DBQueryExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Description: "Run a parameterized SQL query against a configured DSN.",
		WhenToUse:   "Reading from an external user database.",
		Schema:      integration.StructSchema(dbQuerySchema{}),
		Output: map[string]string{
			"rows":      "[]map[string]any — result rows",
			"row_count": "int — row count",
			"columns":   "[]string — column names",
		},
	}
}

// DBQueryExecutor runs a parameterized SQL query against a named
// database. The Database field resolves to an env key whose value is
// the DSN. Supported schemes: postgres://, sqlite:, file:.
//
// Output fields:
//   - rows       — []map[string]any, one entry per row
//   - row_count  — int, same as len(rows)
//   - columns    — []string
type DBQueryExecutor struct{}

// NewDBQueryExecutor builds the executor.
func NewDBQueryExecutor() *DBQueryExecutor { return &DBQueryExecutor{} }

// Execute connects, runs the SQL, and returns rows.
func (e *DBQueryExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	rctx := rc.RenderCtx()

	// Resolve DSN: Database field is an env key name.
	dsn := ""
	if n.Database != "" {
		dsn = rc.EnvValues[n.Database]
		if dsn == "" {
			// Fallback: treat Database as a literal DSN if it contains "://".
			if strings.Contains(n.Database, "://") || strings.HasPrefix(n.Database, "file:") {
				dsn = n.Database
			}
		}
	}
	if dsn == "" {
		return workflow.NodeOutput{}, fmt.Errorf("db_query: database %q not found in workflow env", n.Database)
	}

	// Render SQL template.
	query, err := template.Render(n.SQL, rctx)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("db_query: sql render: %w", err)
	}
	if query == "" {
		return workflow.NodeOutput{}, fmt.Errorf("db_query: sql is empty")
	}

	// Render positional args.
	args := make([]any, 0, len(n.SQLArgs))
	for i, raw := range n.SQLArgs {
		rendered, err := template.Render(raw, rctx)
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("db_query: sql_args[%d] render: %w", i, err)
		}
		args = append(args, rendered)
	}

	// Open connection. Use pgx for postgres, go-sqlite for sqlite/file.
	driverName := "pgx"
	if strings.HasPrefix(dsn, "sqlite:") || strings.HasPrefix(dsn, "file:") {
		driverName = "sqlite"
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("db_query: open: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("db_query: exec: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("db_query: columns: %w", err)
	}

	var result []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("db_query: scan: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			v := vals[i]
			// Convert []byte to string for readability in templates.
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[col] = v
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("db_query: rows: %w", err)
	}

	colNames := make([]any, len(cols))
	for i, c := range cols {
		colNames[i] = c
	}

	return workflow.NodeOutput{
		Fields: map[string]any{
			"rows":      result,
			"row_count": len(result),
			"columns":   colNames,
		},
	}, nil
}
