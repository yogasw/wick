// Package postgres wraps a PostgreSQL database as a read-only wick connector.
// One instance = one database (connection string). Only SELECT and EXPLAIN
// queries are permitted — write operations are blocked at the service layer
// so admins can safely expose a replica without risk of accidental mutation.
//
// File layout:
//
//   - connector.go — Meta, Configs, Input structs, Operations, thin handlers
//   - service.go   — query validation (SELECT-only guard), row limit enforcement
//   - repo.go      — pgx connection pool, query execution
package postgres

import (
	"fmt"

	"github.com/yogasw/wick/pkg/connector"
)

const Key = "postgres"

const defaultMaxRows = 1000

// Configs is the per-instance credential set.
type Configs struct {
	DSN     string `wick:"secret;required;desc=PostgreSQL connection string. Example: postgres://user:pass@host:5432/dbname?sslmode=require"`
	MaxRows int    `wick:"desc=Maximum rows returned per query. Default: 1000. Hard cap: 10000."`
}

// QueryInput runs a SELECT query.
type QueryInput struct {
	SQL string `wick:"textarea;required;desc=SELECT query to execute. Only SELECT statements are allowed. Do not include LIMIT — it is added automatically based on the max_rows config."`
}

// ExplainInput runs EXPLAIN ANALYZE on a query.
type ExplainInput struct {
	SQL string `wick:"textarea;required;desc=Query to analyse. EXPLAIN ANALYZE is prepended automatically."`
}

// Meta returns the static metadata block for this connector.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "PostgreSQL",
		Description: "Run read-only SELECT queries and EXPLAIN ANALYZE on a PostgreSQL database. Write operations are blocked.",
		Icon:        "🐘",
	}
}

// Operations returns the LLM-callable actions for this connector.
func Operations() []connector.Operation {
	return []connector.Operation{
		connector.Op(
			"query",
			"Run Query",
			"Execute a SELECT query and return rows as a JSON array. A LIMIT is appended automatically (default: 1000, max: 10000). Only SELECT statements are permitted.",
			QueryInput{},
			runQuery,
		),
		connector.Op(
			"explain",
			"Explain Query",
			"Run EXPLAIN ANALYZE on a query and return the query plan. Useful for diagnosing slow queries.",
			ExplainInput{},
			runExplain,
		),
	}
}

// ── Operation handlers ───────────────────────────────────────────────

func runQuery(c *connector.Ctx) (any, error) {
	sql := c.Input("sql")
	if err := validateSelectOnly(sql); err != nil {
		return nil, err
	}
	maxRows := resolveMaxRows(c)
	limited := appendLimit(sql, maxRows)

	rows, err := execQuery(c, limited)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"rows":      rows,
		"row_count": len(rows),
		"limit":     maxRows,
	}, nil
}

func runExplain(c *connector.Ctx) (any, error) {
	sql := c.Input("sql")
	if sql == "" {
		return nil, fmt.Errorf("sql is required")
	}
	plan, err := execQuery(c, "EXPLAIN ANALYZE "+sql)
	if err != nil {
		return nil, err
	}
	return map[string]any{"plan": plan}, nil
}
