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
// pgx flushes the offending statement from a connection's cache when it
// fails, so a retry on that connection re-plans against the current
// schema (TestStmtCacheInvalidation in jackc/pgx). But database/sql hands
// the retry ANY free connection, and after a schema change every pooled
// connection holds the stale plan. That is exactly the zero-downtime
// handover: the successor runs AutoMigrate while the old process is still
// serving and draining, so the old process's whole pool goes stale at
// once (0.1.524 added agent_personas.captain_policy; the Team page then
// showed 0A000 for ~10 minutes until the drain ended). One retry landed
// on the next stale connection and surfaced the error.
//
// So retry up to staleRetries times: each failure cleans one connection,
// and the idle pool is smaller than that. Still bounded, so a schema that
// is rewritten continuously surfaces instead of looping forever.
const staleRetries = 12

type stalePlanPool struct {
	gorm.ConnPool
}

// wrapStalePlan puts stalePlanPool under db. gorm.Open copies ConnPool
// into db.Statement, and every session reads its pool from there, so
// both have to change: setting db.ConnPool alone leaves every query on
// the bare pool.
func wrapStalePlan(db *gorm.DB) {
	p := stalePlanPool{ConnPool: db.Statement.ConnPool}
	db.ConnPool = p
	db.Statement.ConnPool = p
}

// BeginTx starts a transaction on the wrapped pool. Statements inside it
// are not retried: after an error Postgres aborts the transaction, so
// only the caller can run it again.
func (p stalePlanPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	switch b := p.ConnPool.(type) {
	case gorm.TxBeginner:
		return b.BeginTx(ctx, opts)
	case gorm.ConnPoolBeginner:
		return b.BeginTx(ctx, opts)
	}
	return nil, gorm.ErrInvalidTransaction
}

// GetDBConn hands db.DB() the *sql.DB under the wrapper.
func (p stalePlanPool) GetDBConn() (*sql.DB, error) {
	switch c := p.ConnPool.(type) {
	case *sql.DB:
		return c, nil
	case gorm.GetDBConnector:
		return c.GetDBConn()
	}
	return nil, gorm.ErrInvalidDB
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
	for i := 0; i < staleRetries && isStalePlan(err); i++ {
		logRetry(query)
		res, err = p.ConnPool.ExecContext(ctx, query, args...)
	}
	return res, err
}

func (p stalePlanPool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := p.ConnPool.QueryContext(ctx, query, args...)
	for i := 0; i < staleRetries; i++ {
		if !p.stale(rows, err) {
			break
		}
		logRetry(query)
		rows, err = p.ConnPool.QueryContext(ctx, query, args...)
	}
	return rows, err
}

// stale reports whether a QueryContext result hit a stale plan, closing
// the rows when it did.
func (stalePlanPool) stale(rows *sql.Rows, err error) bool {
	// A stale plan can surface either from QueryContext itself or from
	// rows.Err() once the result set is walked, depending on where the
	// protocol notices the mismatch. Checking both is what makes the
	// retry reliable; rows.Err() alone is the case pgx's own test hits.
	if err == nil && rows != nil {
		if rerr := rows.Err(); isStalePlan(rerr) {
			rows.Close()
			return true
		}
	}
	return isStalePlan(err)
}

func (p stalePlanPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	row := p.ConnPool.QueryRowContext(ctx, query, args...)
	for i := 0; i < staleRetries && row != nil && isStalePlan(row.Err()); i++ {
		logRetry(query)
		row = p.ConnPool.QueryRowContext(ctx, query, args...)
	}
	return row
}

// PrepareContext is deliberately not retried. A prepared statement is
// handed to the caller and executed later, so a retry here would return a
// statement bound to a connection whose plan we never validated. Callers
// that execute it go through ExecContext/QueryContext above anyway.
