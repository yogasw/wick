package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// fakePool records how many times each method was called and fails the
// first call with a caller-supplied error. It stands in for a real
// connection so the retry logic is testable without a live Postgres.
type fakePool struct {
	execCalls  int
	queryCalls int
	rowCalls   int
	// failFirstWith is returned by the first call to each method; later
	// calls succeed. Mirrors pgx flushing the bad statement from the
	// connection's cache, so the retry sees a working connection.
	failFirstWith error
}

func (f *fakePool) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return nil, nil
}

func (f *fakePool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	f.execCalls++
	if f.execCalls == 1 && f.failFirstWith != nil {
		return nil, f.failFirstWith
	}
	return nil, nil
}

func (f *fakePool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	f.queryCalls++
	if f.queryCalls == 1 && f.failFirstWith != nil {
		return nil, f.failFirstWith
	}
	return nil, nil
}

func (f *fakePool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	f.rowCalls++
	return nil
}

// stalePlanErr builds the error Postgres actually sends when a cached
// plan no longer matches the table, so the test exercises the same
// errors.As path production takes.
func stalePlanErr() error {
	return &pgconn.PgError{
		Code:    sqlStateInvalidCachedPlan,
		Message: "cached plan must not change result type",
	}
}

func TestStalePlanRetriesOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(p stalePlanPool) error
		got  func(f *fakePool) int
	}{
		{
			name: "exec",
			call: func(p stalePlanPool) error {
				_, err := p.ExecContext(context.Background(), "SELECT 1")
				return err
			},
			got: func(f *fakePool) int { return f.execCalls },
		},
		{
			name: "query",
			call: func(p stalePlanPool) error {
				_, err := p.QueryContext(context.Background(), "SELECT 1")
				return err
			},
			got: func(f *fakePool) int { return f.queryCalls },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakePool{failFirstWith: stalePlanErr()}
			p := stalePlanPool{ConnPool: f}

			if err := tc.call(p); err != nil {
				t.Fatalf("expected the retry to succeed, got: %v", err)
			}
			if n := tc.got(f); n != 2 {
				t.Fatalf("expected 2 calls (original + one retry), got %d", n)
			}
		})
	}
}

// A non-0A000 failure is a real query error. Retrying it would double
// every failing write, so it must pass straight through untouched.
func TestStalePlanDoesNotRetryOtherErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "plain error", err: errors.New("connection refused")},
		{
			name: "other pg error",
			err:  &pgconn.PgError{Code: "23505", Message: "duplicate key value"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakePool{failFirstWith: tc.err}
			p := stalePlanPool{ConnPool: f}

			_, err := p.ExecContext(context.Background(), "INSERT INTO t VALUES (1)")
			if err == nil {
				t.Fatal("expected the original error to propagate, got nil")
			}
			if !errors.Is(err, tc.err) {
				t.Fatalf("expected the original error, got: %v", err)
			}
			if f.execCalls != 1 {
				t.Fatalf("expected exactly 1 call (no retry), got %d", f.execCalls)
			}
		})
	}
}

// The 0A000 check has to survive wrapping, since gorm and database/sql
// both return errors that have passed through several layers.
func TestIsStalePlanThroughWrappedError(t *testing.T) {
	wrapped := fmt.Errorf("gorm: %w", fmt.Errorf("driver: %w", stalePlanErr()))
	if !isStalePlan(wrapped) {
		t.Fatal("expected a wrapped 0A000 to be detected")
	}
	if isStalePlan(nil) {
		t.Fatal("expected nil to not be treated as a stale plan")
	}
}

// A second consecutive 0A000 means something is rewriting the schema
// continuously. That is a real bug, so the error must surface rather
// than the wrapper retrying forever.
func TestStalePlanGivesUpAfterOneRetry(t *testing.T) {
	// alwaysFails never recovers, unlike fakePool.
	always := &alwaysStalePool{}
	p := stalePlanPool{ConnPool: always}

	_, err := p.ExecContext(context.Background(), "SELECT 1")
	if !isStalePlan(err) {
		t.Fatalf("expected the second 0A000 to propagate, got: %v", err)
	}
	if always.calls != 2 {
		t.Fatalf("expected exactly 2 calls (no infinite retry), got %d", always.calls)
	}
}

type alwaysStalePool struct {
	calls int
	gorm.ConnPool
}

func (a *alwaysStalePool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	a.calls++
	return nil, stalePlanErr()
}
