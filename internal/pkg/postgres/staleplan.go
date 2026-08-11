package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// sqlStateInvalidCachedPlan is what Postgres returns when a connection's
// cached statement plan no longer matches the table it was planned
// against: SQLSTATE 0A000, "cached plan must not change result type".
//
// It means the schema changed under a live connection — a DDL pass ran
// while this connection was open. It never means the query is wrong.
const sqlStateInvalidCachedPlan = "0A000"

// stalePlanPool retries a query once when the connection it ran on had a
// stale plan cached.
//
// Migrate already prevents the usual cause by running DDL on a connection
// of its own and guarding the pass with a sync.Once, so nothing in a
// live pool outlives the schema it was planned against. This is the net
// underneath that: if the boot order changes again, or a migration runs
// from outside the process, the symptom stays invisible to users instead
// of surfacing as an intermittent 500.
//
// Retrying on the same connection is correct — pgx flushes the offending
// statement from that connection's cache when it fails, so the second
// attempt re-plans against the current schema. That is pgx's documented
// behaviour (see TestStmtCacheInvalidation in jackc/pgx), and it is why
// this needs no way to evict a connection from database/sql's pool.
//
// One retry only. A second 0A000 means something is rewriting the schema
// continuously, which is a real bug worth surfacing rather than hiding.
type stalePlanPool struct {
	gorm.ConnPool
}

// isStalePlan reports whether err is Postgres telling us this
// connection's cached plan is stale.
func isStalePlan(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlStateInvalidCachedPlan
}

// logRetry records a retry so an operator can tell "this fired once at
// boot" (expected, harmless) from "this fires constantly" (a real
// migration-ordering bug that the retry is papering over).
func logRetry(query string) {
	l := log.With().Str("component", "db").Logger()
	l.Warn().Str("query", query).
		Msg("stale cached plan (0A000) — retrying once; schema changed under a live connection")
}

func (p stalePlanPool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := p.ConnPool.ExecContext(ctx, query, args...)
	if isStalePlan(err) {
		logRetry(query)
		return p.ConnPool.ExecContext(ctx, query, args...)
	}
	return res, err
}

func (p stalePlanPool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := p.ConnPool.QueryContext(ctx, query, args...)
	// A stale plan can surface either from QueryContext itself or from
	// rows.Err() once the result set is walked, depending on where the
	// protocol notices the mismatch. Checking both is what makes the
	// retry reliable; rows.Err() alone is the case pgx's own test hits.
	if err == nil && rows != nil {
		if rerr := rows.Err(); isStalePlan(rerr) {
			rows.Close()
			logRetry(query)
			return p.ConnPool.QueryContext(ctx, query, args...)
		}
	}
	if isStalePlan(err) {
		logRetry(query)
		return p.ConnPool.QueryContext(ctx, query, args...)
	}
	return rows, err
}

func (p stalePlanPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	row := p.ConnPool.QueryRowContext(ctx, query, args...)
	if row != nil && isStalePlan(row.Err()) {
		logRetry(query)
		return p.ConnPool.QueryRowContext(ctx, query, args...)
	}
	return row
}

// PrepareContext is deliberately not retried. A prepared statement is
// handed to the caller and executed later, so a retry here would return a
// statement bound to a connection whose plan we never validated. Callers
// that execute it go through ExecContext/QueryContext above anyway.
