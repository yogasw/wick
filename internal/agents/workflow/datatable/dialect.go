package datatable

import (
	"fmt"

	"gorm.io/gorm"
)

// dialect renders the handful of SQL constructs that differ between the
// databases wick supports.
//
// It exists because PgService — despite the name — is wired for EVERY
// dialect: setup.Manager.WithDataTablesDB calls NewPg(db) unconditionally
// and wick's own gorm.go accepts both postgres and sqlite. The JSONB
// spellings this package was written with (`::` casts, the `?` key
// existence operator, `-` key removal, ILIKE, now()) are postgres-only,
// and on sqlite they surface as bare parser errors far from their cause:
// `::jsonb` gives `unrecognized token: ":"`, and the `?` operator
// collides with sqlite's own positional placeholder.
//
// Every method returns a fragment, never a whole statement, so the two
// dialects share one query builder rather than forking it.
type dialect struct{ sqlite bool }

func dialectOf(db *gorm.DB) dialect {
	return dialect{sqlite: db != nil && db.Dialector != nil && db.Dialector.Name() == "sqlite"}
}

// jsonPath renders the sqlite JSON path for a column id. Column ids are
// generated (`c1`, `c2`, …) and validated upstream, so quoting is only
// belt-and-braces against a future id shape.
func jsonPath(cid string) string { return fmt.Sprintf(`$."%s"`, cid) }

// text renders "read column <cid> out of data as a comparable value".
// `->>` is shared: postgres has had it since 9.3, sqlite since 3.38.
func (d dialect) text(cid string) string { return fmt.Sprintf(`(data->>'%s')`, cid) }

// cast wraps an expression so comparisons use the column's own ordering
// rather than text ordering — without it "9" > "10" is true.
//
// postgres casts with a `::` suffix; sqlite has no such operator and
// spells it CAST(x AS t). sqlite has no boolean or timestamp type: JSON
// booleans already come back as 0/1 through `->>`, and ISO-8601
// timestamps sort correctly as text, so both are left alone.
func (d dialect) cast(inner, colType string) string {
	if !d.sqlite {
		switch colType {
		case "int":
			return inner + "::bigint"
		case "float":
			return inner + "::double precision"
		case "bool":
			return inner + "::boolean"
		case "timestamp":
			return inner + "::timestamptz"
		}
		return inner
	}
	switch colType {
	case "int":
		return "CAST(" + inner + " AS INTEGER)"
	case "float":
		return "CAST(" + inner + " AS REAL)"
	}
	return inner
}

// keyExists renders "data actually carries this key", which is different
// from "its value is null" and is what is_empty / is_not_empty turn on.
//
// The postgres form embeds the id as a literal on purpose: `?` is the
// jsonb existence OPERATOR here, and passing the id as a bind parameter
// would make the driver read that operator as a placeholder.
func (d dialect) keyExists(cid string) string {
	if d.sqlite {
		return fmt.Sprintf(`json_type(data, '%s') IS NOT NULL`, jsonPath(cid))
	}
	return fmt.Sprintf(`(data ? '%s')`, cid)
}

// iLike is the case-insensitive LIKE operator. sqlite's LIKE is already
// case-insensitive for ASCII, so the behaviour matches without ILIKE,
// which it does not have.
func (d dialect) iLike() string {
	if d.sqlite {
		return "LIKE"
	}
	return "ILIKE"
}

// removeKeyExpr renders the SET fragment that drops one key from data,
// plus the argument it binds.
//
// postgres binds the key to the `-` operator; sqlite takes a JSON path,
// so the two differ in the ARGUMENT as well as the expression — hence
// returning both rather than only the SQL.
func (d dialect) removeKeyExpr(cid string) (string, []any) {
	if d.sqlite {
		return `json_remove("data", ?)`, []any{jsonPath(cid)}
	}
	return `"data" - ?`, []any{cid}
}

// jsonMerge renders "patch the stored JSON with this payload".
//
// Both branches stay a SINGLE statement on purpose. Reading the row into
// Go, merging there and writing it back would be dialect-neutral too,
// but it turns an atomic patch into read-modify-write — and lockMeta's
// FOR UPDATE uses the GORM v1 `gorm:query_option` idiom that v2 ignores,
// so no row lock actually holds concurrent upserts apart. That would
// trade a syntax error for a lost update.
//
// One semantic difference is unavoidable: postgres `||` stores an
// explicit JSON null as a value, while sqlite's json_patch treats null
// as "delete this key" (RFC 7386). Payloads here come from normalizeRow
// + Encode, which omit unset columns rather than emitting nulls, so the
// two agree on everything this path actually produces.
func (d dialect) jsonMerge(payload string) (string, []any) {
	if d.sqlite {
		return `json_patch(COALESCE("data", '{}'), ?)`, []any{payload}
	}
	return `COALESCE("data", '{}'::jsonb) || ?::jsonb`, []any{payload}
}
