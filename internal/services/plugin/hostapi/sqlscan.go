package hostapi

import (
	"fmt"
	"slices"
	"strings"
)

// sqlInfo is what Query needs to know about one statement before it runs.
type sqlInfo struct {
	// write: the statement (or a CTE in it) inserts, updates or deletes.
	write bool
	// reads and writes are the tables it reads and the ones it changes,
	// lower-cased, "public." stripped, CTE names left out.
	reads, writes []string
}

type sqlTokKind int

const (
	tokWord  sqlTokKind = iota // keyword or unquoted identifier (lower-cased)
	tokIdent                   // "quoted identifier" (case kept)
	tokPunct                   // ( ) , ; . and any other operator byte
	tokValue                   // string, number or $n parameter
)

type sqlTok struct {
	kind sqlTokKind
	text string
}

func (t sqlTok) is(word string) bool       { return t.kind == tokWord && t.text == word }
func (t sqlTok) punct(p string) bool       { return t.kind == tokPunct && t.text == p }
func (t sqlTok) name() bool                { return t.kind == tokWord || t.kind == tokIdent }
func isWordStart(c byte) bool              { return c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') || c >= 0x80 }
func isWordByte(c byte) bool               { return isWordStart(c) || (c >= '0' && c <= '9') || c == '$' }
func isDigit(c byte) bool                  { return c >= '0' && c <= '9' }
func sqlErr(format string, a ...any) error { return fmt.Errorf(format, a...) }

// tokenizeSQL splits sql into tokens, dropping comments and whitespace and
// collapsing string literals (standard, E”, dollar-quoted) into one value
// token, so a ';' or a keyword inside a literal is never seen as SQL.
func tokenizeSQL(sql string) ([]sqlTok, error) {
	var out []sqlTok
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			i++
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			// Postgres ends a line comment at \n or \r.
			for i < len(sql) && sql[i] != '\n' && sql[i] != '\r' {
				i++
			}
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			// Block comments nest in Postgres: /* /* */ still open */.
			depth := 0
			for {
				if i+1 >= len(sql) {
					return nil, sqlErr("unterminated comment")
				}
				switch {
				case sql[i] == '/' && sql[i+1] == '*':
					depth++
					i += 2
				case sql[i] == '*' && sql[i+1] == '/':
					depth--
					i += 2
				default:
					i++
					continue
				}
				if depth == 0 {
					break
				}
			}
		case (c == 'u' || c == 'U') && i+1 < len(sql) && sql[i+1] == '&':
			return nil, sqlErr("U& escaped strings and identifiers are not allowed")
		case c == '\'' || ((c == 'e' || c == 'E') && i+1 < len(sql) && sql[i+1] == '\''):
			backslash := c != '\''
			if backslash {
				i++
			}
			j := i + 1
			for ; j < len(sql); j++ {
				if backslash && sql[j] == '\\' {
					j++
					continue
				}
				if sql[j] == '\'' {
					if j+1 < len(sql) && sql[j+1] == '\'' {
						j++
						continue
					}
					break
				}
			}
			if j >= len(sql) {
				return nil, sqlErr("unterminated string literal")
			}
			out = append(out, sqlTok{tokValue, "'"})
			i = j + 1
		case c == '"':
			var b strings.Builder
			j := i + 1
			for ; j < len(sql); j++ {
				if sql[j] == '"' {
					if j+1 < len(sql) && sql[j+1] == '"' {
						b.WriteByte('"')
						j++
						continue
					}
					break
				}
				b.WriteByte(sql[j])
			}
			if j >= len(sql) {
				return nil, sqlErr("unterminated quoted identifier")
			}
			out = append(out, sqlTok{tokIdent, b.String()})
			i = j + 1
		case c == '$':
			j := i + 1
			if j < len(sql) && isDigit(sql[j]) {
				for j < len(sql) && isDigit(sql[j]) {
					j++
				}
				out = append(out, sqlTok{tokValue, "$"})
				i = j
				continue
			}
			for j < len(sql) && isWordByte(sql[j]) && sql[j] != '$' {
				j++
			}
			if j >= len(sql) || sql[j] != '$' {
				return nil, sqlErr("unexpected $ in SQL")
			}
			tag := sql[i : j+1]
			end := strings.Index(sql[j+1:], tag)
			if end < 0 {
				return nil, sqlErr("unterminated dollar-quoted string")
			}
			out = append(out, sqlTok{tokValue, "'"})
			i = j + 1 + end + len(tag)
		case isWordStart(c):
			j := i
			for j < len(sql) && isWordByte(sql[j]) {
				j++
			}
			out = append(out, sqlTok{tokWord, strings.ToLower(sql[i:j])})
			i = j
		case isDigit(c):
			j := i
			for j < len(sql) && (isWordByte(sql[j]) || sql[j] == '.') {
				j++
			}
			out = append(out, sqlTok{tokValue, "0"})
			i = j
		default:
			out = append(out, sqlTok{tokPunct, string(c)})
			i++
		}
	}
	return out, nil
}

// sqlFromEnds are the words that end a FROM list.
var sqlFromEnds = map[string]bool{
	"where": true, "group": true, "order": true, "limit": true, "having": true, "window": true,
	"union": true, "intersect": true, "except": true, "returning": true,
	"set": true, "values": true, "select": true, "for": true, "offset": true, "fetch": true,
}

// sqlMainWords start the statement a WITH prelude belongs to.
var sqlMainWords = map[string]bool{"select": true, "insert": true, "update": true, "delete": true, "values": true, "table": true}

// sqlFuncsWithFrom take FROM inside their argument list (EXTRACT(x FROM y)).
var sqlFuncsWithFrom = map[string]bool{"extract": true, "substring": true, "trim": true, "overlay": true, "position": true}

// sqlParenWords are keywords a '(' may follow without it being a function
// call (IN (...), EXISTS (...), OVER (...), VALUES (...), ...).
var sqlParenWords = setOf("select", "from", "where", "and", "or", "not", "in", "exists", "any", "all", "some",
	"as", "on", "using", "join", "values", "with", "recursive", "union", "intersect", "except", "by", "over",
	"filter", "within", "group", "having", "when", "then", "else", "case", "is", "like", "ilike", "similar",
	"between", "returning", "set", "into", "limit", "offset", "distinct", "lateral", "array", "row", "only",
	"materialized", "do", "conflict", "for", "order", "partition", "rows", "range", "escape", "to", "fetch",
	"table", "cross", "inner", "outer", "full", "natural")

// sqlSafeFuncs is the allowlist of functions a Query may call: aggregates,
// window, string, date/time, json/jsonb, math, conditional and array
// helpers that read nothing but their arguments. Anything else is refused
// (fail closed): functions that run SQL from a string (ts_stat), touch
// sequences (nextval, setval), server files, sessions or settings (pg_*,
// set_config), other databases (dblink) or large objects (lo_*).
var sqlSafeFuncs = setOf(
	// aggregates and window functions
	"count", "sum", "avg", "min", "max", "array_agg", "string_agg", "json_agg", "jsonb_agg",
	"json_object_agg", "jsonb_object_agg", "bool_and", "bool_or", "every", "bit_and", "bit_or",
	"row_number", "rank", "dense_rank", "percent_rank", "cume_dist", "ntile", "lag", "lead",
	"first_value", "last_value", "nth_value", "percentile_cont", "percentile_disc", "mode",
	// strings
	"lower", "upper", "length", "char_length", "character_length", "octet_length", "substr", "substring",
	"trim", "ltrim", "rtrim", "btrim", "replace", "concat", "concat_ws", "left", "right", "lpad", "rpad",
	"position", "strpos", "split_part", "starts_with", "initcap", "reverse", "repeat", "format", "md5",
	"regexp_replace", "regexp_match", "regexp_matches", "regexp_split_to_array", "translate", "overlay",
	"to_char", "to_number", "encode", "decode", "ifnull", "instr",
	// date and time
	"now", "date_trunc", "date_part", "extract", "age", "to_timestamp", "to_date", "make_date",
	"make_time", "make_timestamp", "make_timestamptz", "make_interval", "clock_timestamp",
	"statement_timestamp", "transaction_timestamp", "timezone", "justify_days", "justify_hours",
	"justify_interval", "date", "datetime", "strftime", "julianday",
	// json / jsonb
	"json_build_object", "jsonb_build_object", "json_build_array", "jsonb_build_array", "to_json", "to_jsonb",
	"json_extract_path", "json_extract_path_text", "jsonb_extract_path", "jsonb_extract_path_text",
	"json_array_length", "jsonb_array_length", "json_typeof", "jsonb_typeof", "jsonb_set", "jsonb_insert",
	"jsonb_strip_nulls", "json_strip_nulls", "jsonb_pretty", "json_array_elements", "jsonb_array_elements",
	"json_array_elements_text", "jsonb_array_elements_text", "json_each", "jsonb_each", "json_each_text",
	"jsonb_each_text", "json_object_keys", "jsonb_object_keys", "jsonb_path_query", "jsonb_path_exists",
	"json_extract",
	// math
	"abs", "ceil", "ceiling", "floor", "round", "trunc", "mod", "power", "sqrt", "sign", "div", "exp",
	"ln", "log", "random", "width_bucket",
	// conditional and casts
	"coalesce", "nullif", "greatest", "least", "cast",
	// arrays and set-returning helpers
	"array_length", "array_to_string", "unnest", "cardinality", "array_position", "array_positions",
	"array_remove", "array_append", "array_prepend", "array_cat", "string_to_array", "generate_series",
)

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// scanSQL reads one statement well enough to gate it: exactly one statement
// (a trailing ';' is fine), of a kind Query runs (SELECT, WITH, VALUES,
// TABLE, INSERT, UPDATE, DELETE), with the tables it reads and writes. It is
// deliberately simple and fails closed: a word it takes for a table that is
// not one is reported as undeclared, never let through.
func scanSQL(sql string) (*sqlInfo, error) {
	toks, err := tokenizeSQL(sql)
	if err != nil {
		return nil, err
	}
	for len(toks) > 0 && toks[len(toks)-1].punct(";") {
		toks = toks[:len(toks)-1]
	}
	if len(toks) == 0 {
		return nil, sqlErr("empty statement")
	}
	if slices.ContainsFunc(toks, func(t sqlTok) bool { return t.punct(";") }) {
		return nil, sqlErr("only one statement per query")
	}
	switch toks[0].text {
	case "select", "with", "values", "table", "insert", "update", "delete":
		if toks[0].kind != tokWord {
			return nil, sqlErr("statement must start with SELECT, WITH, VALUES, TABLE, INSERT, UPDATE or DELETE")
		}
	default:
		return nil, sqlErr("statement must start with SELECT, WITH, VALUES, TABLE, INSERT, UPDATE or DELETE")
	}
	info := &sqlInfo{}
	type paren struct {
		fn string
		id int
	}
	var stack []paren
	// ctes maps a CTE name to the paren groups (0 = top level) whose WITH
	// defined it; a name is a CTE only inside one of those groups.
	ctes := map[string][]int{}
	isCTE := func(name string) bool {
		for _, g := range ctes[name] {
			if g == 0 || slices.ContainsFunc(stack, func(p paren) bool { return p.id == g }) {
				return true
			}
		}
		return false
	}
	group := func() int {
		if len(stack) == 0 {
			return 0
		}
		return stack[len(stack)-1].id
	}
	nextID := 0
	type cteDef struct {
		name  string
		group int
	}
	pending := map[int][]cteDef{} // closing-paren index of a CTE body -> CTEs it completes
	// matching returns the index of the ')' closing the '(' at i.
	matching := func(i int) int {
		depth := 0
		for j := i; j < len(toks); j++ {
			switch {
			case toks[j].punct("("):
				depth++
			case toks[j].punct(")"):
				depth--
				if depth == 0 {
					return j
				}
			}
		}
		return -1
	}
	// tableAt reads a (schema.)name at i; ok=false when i is no name (a
	// subquery, a VALUES list) or the name is a function call. With
	// columns, a '(' after the name is an INSERT column list instead.
	tableAt := func(i int, columns bool) (name string, ok bool, err error) {
		for i < len(toks) && (toks[i].is("only") || toks[i].is("lateral")) {
			i++
		}
		if i >= len(toks) || !toks[i].name() {
			return "", false, nil
		}
		parts := []sqlTok{toks[i]}
		for i+2 < len(toks) && toks[i+1].punct(".") && toks[i+2].name() {
			parts = append(parts, toks[i+2])
			i += 2
		}
		if i+1 < len(toks) && toks[i+1].punct("(") && !columns {
			return "", false, nil // a function call; the main loop gates it
		}
		names := make([]string, len(parts))
		for k, p := range parts {
			names[k] = p.text
		}
		if len(names) == 2 && names[0] == "public" {
			names = names[1:]
		}
		return strings.Join(names, "."), true, nil
	}
	addRead := func(i int) error {
		t, ok, err := tableAt(i, false)
		if err != nil || !ok {
			return err
		}
		if !isCTE(t) && !slices.Contains(info.reads, t) {
			info.reads = append(info.reads, t)
		}
		return nil
	}
	addWrite := func(i int) error {
		t, ok, err := tableAt(i, true)
		if err != nil {
			return err
		}
		if !ok {
			return sqlErr("cannot tell which table the statement writes")
		}
		if isCTE(t) {
			return sqlErr("writing through a CTE name is not supported")
		}
		if !slices.Contains(info.writes, t) {
			info.writes = append(info.writes, t)
		}
		return nil
	}
	// fromDepths are the paren depths of the FROM lists still open; a ','
	// at the innermost one starts another table.
	var fromDepths []int
	inFrom := func() bool { return len(fromDepths) > 0 && fromDepths[len(fromDepths)-1] == len(stack) }
	var withs []int           // paren depth of each open WITH prelude
	notCall := map[int]bool{} // name tokens followed by '(' that are no call
	for i, t := range toks {
		prev := func(n int) sqlTok {
			if i-n < 0 {
				return sqlTok{}
			}
			return toks[i-n]
		}
		switch {
		case t.punct("("):
			fn := ""
			if prev(1).kind == tokWord {
				fn = prev(1).text
			}
			nextID++
			stack = append(stack, paren{fn: fn, id: nextID})
			continue
		case t.punct(")"):
			for _, d := range pending[i] {
				ctes[d.name] = append(ctes[d.name], d.group)
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			for len(fromDepths) > 0 && fromDepths[len(fromDepths)-1] > len(stack) {
				fromDepths = fromDepths[:len(fromDepths)-1]
			}
			for len(withs) > 0 && withs[len(withs)-1] > len(stack) {
				withs = withs[:len(withs)-1]
			}
			continue
		case t.punct(","):
			if inFrom() {
				if err := addRead(i + 1); err != nil {
					return nil, err
				}
			}
			continue
		case t.kind == tokIdent && i+1 < len(toks) && toks[i+1].punct("(") && !notCall[i]:
			return nil, sqlErr("quoted function names are not allowed")
		}
		if t.kind != tokWord {
			continue
		}
		if inFrom() && sqlFromEnds[t.text] {
			fromDepths = fromDepths[:len(fromDepths)-1]
		}
		inWith := len(withs) > 0 && withs[len(withs)-1] == len(stack)
		if inWith && sqlMainWords[t.text] {
			// The statement the WITH prelude belongs to starts here.
			withs = withs[:len(withs)-1]
			inWith = false
		}
		if t.is("with") && (i == 0 || prev(1).punct("(")) {
			withs = append(withs, len(stack))
			continue
		}
		// CTE name, only inside a WITH prelude:
		// (WITH | RECURSIVE | ,) name [(cols)] AS [NOT] [MATERIALIZED] (
		cteName := inWith && (prev(1).is("with") || prev(1).is("recursive") || prev(1).punct(","))
		// A word before '(' is a function call unless it is a keyword, a
		// CTE name or INSERT target with a column list, or a type/alias
		// after AS or '::'. Calls must be on the allowlist and unqualified.
		if i+1 < len(toks) && toks[i+1].punct("(") && !sqlParenWords[t.text] && !cteName && !notCall[i] &&
			!prev(1).is("as") && !prev(1).punct(":") {
			if prev(1).punct(".") {
				return nil, sqlErr("schema-qualified function calls are not allowed")
			}
			if !sqlSafeFuncs[t.text] {
				return nil, sqlErr("function %s is not allowed (not on the Query function allowlist)", t.text)
			}
		}
		if cteName {
			j := i + 1
			if j < len(toks) && toks[j].punct("(") {
				if j = matching(j); j < 0 {
					return nil, sqlErr("unbalanced parentheses")
				}
				j++
			}
			if j < len(toks) && toks[j].is("as") {
				j++
				for j < len(toks) && (toks[j].is("not") || toks[j].is("materialized")) {
					j++
				}
				if j < len(toks) && toks[j].punct("(") {
					// Visible only after its own body: inside it the name
					// still means the real table (a RECURSIVE self-reference
					// is therefore checked as a table and fails closed).
					end := matching(j)
					if end < 0 {
						return nil, sqlErr("unbalanced parentheses")
					}
					pending[end] = append(pending[end], cteDef{t.text, group()})
				}
			}
		}
		switch t.text {
		case "from":
			if prev(1).is("delete") {
				info.write = true
				if err := addWrite(i + 1); err != nil {
					return nil, err
				}
				continue
			}
			if prev(1).is("distinct") || (len(stack) > 0 && sqlFuncsWithFrom[stack[len(stack)-1].fn]) {
				continue
			}
			if !inFrom() {
				fromDepths = append(fromDepths, len(stack))
			}
			if err := addRead(i + 1); err != nil {
				return nil, err
			}
		case "join":
			if err := addRead(i + 1); err != nil {
				return nil, err
			}
		case "using":
			// DELETE ... USING t1, t2 is a FROM list; JOIN ... USING (cols)
			// reads nothing (a '(' is no table).
			if !inFrom() {
				fromDepths = append(fromDepths, len(stack))
			}
			if err := addRead(i + 1); err != nil {
				return nil, err
			}
		case "table":
			// TABLE x anywhere (statement, CTE body, subquery, UNION arm)
			// reads x.
			name, ok, err := tableAt(i+1, false)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, sqlErr("TABLE must name a table")
			}
			if !isCTE(name) && !slices.Contains(info.reads, name) {
				info.reads = append(info.reads, name)
			}
		case "into":
			if !prev(1).is("insert") {
				return nil, sqlErr("SELECT ... INTO is not allowed")
			}
			// The target and its column list are no function call.
			for j := i + 1; j < len(toks) && toks[j].name(); j += 2 {
				notCall[j] = true
				if j+1 >= len(toks) || !toks[j+1].punct(".") {
					break
				}
			}
			info.write = true
			if err := addWrite(i + 1); err != nil {
				return nil, err
			}
		case "update":
			if prev(1).is("for") || prev(1).is("key") || prev(1).is("do") {
				if prev(1).is("do") {
					continue
				}
				return nil, sqlErr("row locks (FOR UPDATE) are not allowed")
			}
			info.write = true
			if err := addWrite(i + 1); err != nil {
				return nil, err
			}
		case "delete":
			info.write = true
		case "share":
			if prev(1).is("for") || prev(1).is("key") {
				return nil, sqlErr("row locks (FOR SHARE) are not allowed")
			}
		}
	}
	return info, nil
}
