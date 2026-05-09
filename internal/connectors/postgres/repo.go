package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/yogasw/wick/pkg/connector"
)

// execQuery opens a single-use connection, runs sql, and returns rows
// as a slice of string-keyed maps. pgx is used directly (not GORM) so
// connectors stay independent of the app's ORM configuration.
//
// Connections are not pooled across calls — each Execute gets its own
// connection and closes it when done. For the low-frequency, interactive
// LLM use case this is fine; connection overhead is negligible compared
// to LLM inference time.
func execQuery(c *connector.Ctx, sql string) ([]map[string]any, error) {
	dsn := strings.TrimSpace(c.Cfg("dsn"))
	if dsn == "" {
		return nil, fmt.Errorf("dsn is not configured for this connector instance")
	}

	conn, err := pgx.Connect(c.Context(), dsn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	rows, err := conn.Query(c.Context(), sql)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	var result []map[string]any

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		row := make(map[string]any, len(fields))
		for i, f := range fields {
			row[string(f.Name)] = values[i]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return result, nil
}
