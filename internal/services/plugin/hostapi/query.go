package hostapi

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// Query limits.
const (
	QueryMaxTimeout = 10 * time.Second
	QueryMaxRows    = 5000
	// queryNoteMax caps the SQL + params written to the audit note, and
	// queryParamMax each param in it.
	queryNoteMax  = 4096
	queryParamMax = 64
)

// Query runs one SQL statement on wick's database. write=false needs db.read
// and runs in a read-only transaction that is always rolled back;
// write=true needs db.write. Every table the statement reads is checked as
// db.read "<table>:read" (also on a write: an admin block on reading a
// table holds for INSERT ... SELECT too), every table it changes as
// db.write "<table>:write"; none may be undeclared or blocked. The SQL and
// params go to the audit note (each param cut to 64 bytes, all of it to
// 4 KB).
//
// Placeholders follow the database dialect: $1, $2, ... on Postgres (what
// production wick runs), ? on SQLite. Params bind as text values.
func (s *Server) Query(ctx context.Context, in *hostpb.QueryRequest) (*hostpb.QueryResponse, error) {
	scope := wickplugin.ScopeHostDBRead
	if in.GetWrite() {
		scope = wickplugin.ScopeHostDBWrite
	}
	info, perr := scanSQL(in.GetSql())
	g := gate{rpc: "Query", scope: scope, note: queryNote(in)}
	if perr != nil {
		g.reject = status.Error(codes.InvalidArgument, "query: "+perr.Error())
	}
	var out *hostpb.QueryResponse
	err := s.runGate(ctx, g, func(c *call) error {
		if s.d.DB == nil {
			return unavailable("Query")
		}
		if info.write && !in.GetWrite() {
			return &host.PermissionDeniedError{Scope: wickplugin.ScopeHostDBWrite, Plugin: c.plugin.Key,
				Message: "statement changes data; send write=true (needs db.write)"}
		}
		check := func(sc, table, mode string) error {
			item := wickplugin.DBItem(table, mode)
			if dec := s.d.Perms.Decide(ctx, c.plugin, sc, item); !dec.Allowed {
				return &host.PermissionDeniedError{Scope: sc, Item: item, Plugin: c.plugin.Key, Message: dec.Reason}
			}
			return nil
		}
		for _, t := range info.reads {
			if err := check(wickplugin.ScopeHostDBRead, t, "read"); err != nil {
				return err
			}
		}
		for _, t := range info.writes {
			if err := check(wickplugin.ScopeHostDBWrite, t, "write"); err != nil {
				return err
			}
		}
		timeout := QueryMaxTimeout
		if ms := in.GetTimeoutMs(); ms > 0 && time.Duration(ms)*time.Millisecond < timeout {
			timeout = time.Duration(ms) * time.Millisecond
		}
		maxRows := QueryMaxRows
		if n := int(in.GetMaxRows()); n > 0 && n < maxRows {
			maxRows = n
		}
		// The context outlives the server-side timeout a little, so on
		// Postgres statement_timeout reports first.
		ctx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
		defer cancel()
		var err error
		out, err = runQuery(ctx, s.d.DB, in, timeout, maxRows)
		return err
	})
	return out, err
}

// queryNote is the audit note of a Query: its SQL and params, capped.
func queryNote(in *hostpb.QueryRequest) string {
	n := "sql: " + in.GetSql()
	if len(in.GetArgs()) > 0 {
		params := make([]string, len(in.GetArgs()))
		for i, a := range in.GetArgs() {
			params[i] = a
			if c := clip(a, queryParamMax); c != a {
				params[i] = c + "…"
			}
		}
		p, _ := json.Marshal(params)
		n += " params: " + string(p)
	}
	if len(n) > queryNoteMax {
		n = n[:queryNoteMax]
		for !utf8.ValidString(n) {
			n = n[:len(n)-1]
		}
		n += "…"
	}
	return n
}

// isPostgres reports whether db is Postgres. Only Postgres enforces a
// READ ONLY transaction and statement_timeout; other dialects (SQLite in
// tests and single-node installs) rely on the statement gate, the
// always-rolled-back read transaction and the context deadline.
func isPostgres(db *gorm.DB) bool {
	return db.Dialector != nil && db.Dialector.Name() == "postgres"
}

// beginQueryTx opens the transaction a Query runs in.
func beginQueryTx(ctx context.Context, db *gorm.DB, write bool, timeout time.Duration) (*sql.Tx, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	pg := isPostgres(db)
	// Postgres: BEGIN READ ONLY. database/sql drivers for other dialects
	// may refuse the option, so it is only asked of Postgres.
	tx, err := sqlDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: pg && !write})
	if err != nil {
		return nil, err
	}
	if pg {
		if _, err := tx.ExecContext(ctx, "SET LOCAL statement_timeout = "+strconv.FormatInt(timeout.Milliseconds(), 10)); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	return tx, nil
}

func runQuery(ctx context.Context, db *gorm.DB, in *hostpb.QueryRequest, timeout time.Duration, maxRows int) (*hostpb.QueryResponse, error) {
	tx, err := beginQueryTx(ctx, db, in.GetWrite(), timeout)
	if err != nil {
		return nil, queryErr(ctx, err)
	}
	defer func() { _ = tx.Rollback() }()
	args := make([]any, len(in.GetArgs()))
	for i, a := range in.GetArgs() {
		args[i] = a
	}
	if in.GetWrite() {
		res, err := tx.ExecContext(ctx, in.GetSql(), args...)
		if err != nil {
			return nil, queryErr(ctx, err)
		}
		n, _ := res.RowsAffected()
		if err := tx.Commit(); err != nil {
			return nil, queryErr(ctx, err)
		}
		return &hostpb.QueryResponse{RowsAffected: n}, nil
	}
	rows, err := tx.QueryContext(ctx, in.GetSql(), args...)
	if err != nil {
		return nil, queryErr(ctx, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, queryErr(ctx, err)
	}
	out := &hostpb.QueryResponse{Columns: cols}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if len(out.Rows) == maxRows {
			out.Truncated = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, queryErr(ctx, err)
		}
		row := &hostpb.Row{Values: make([]string, len(cols)), Nulls: make([]bool, len(cols))}
		for i, v := range vals {
			row.Values[i], row.Nulls[i] = sqlString(v)
		}
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, queryErr(ctx, err)
	}
	return out, nil
}

// sqlString renders one scanned value; null reports SQL NULL. Binary
// values ([]byte: bytea, BLOB) come back as "\x" + lower-case hex, the
// Postgres bytea text form, so a row is always valid UTF-8.
func sqlString(v any) (s string, null bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case []byte:
		return `\x` + hex.EncodeToString(x), false
	case string:
		return x, false
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano), false
	case bool:
		return strconv.FormatBool(x), false
	default:
		return fmt.Sprint(x), false
	}
}

// queryErr maps a database error: a deadline is DeadlineExceeded, anything
// else InvalidArgument with the database's message (a bad query, a read-only
// violation, a statement timeout).
func queryErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return status.Error(codes.DeadlineExceeded, "query: "+ctx.Err().Error())
	}
	msg := err.Error()
	if strings.Contains(msg, "statement timeout") {
		return status.Error(codes.DeadlineExceeded, "query: "+msg)
	}
	return status.Error(codes.InvalidArgument, "query: "+msg)
}
