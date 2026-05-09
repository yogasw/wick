package postgres

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

const hardCapRows = 10000

// validateSelectOnly blocks any SQL that is not a SELECT statement.
// This is a defence-in-depth check on top of DB user privileges —
// connectors should only expose read replicas, but we also reject
// mutations at the application layer.
func validateSelectOnly(sql string) error {
	clean := strings.TrimSpace(sql)
	if clean == "" {
		return fmt.Errorf("sql is required")
	}
	// Strip leading comments so "-- hack\nDROP TABLE" doesn't sneak through.
	noComment := stripLeadingComments(clean)
	upper := strings.ToUpper(strings.TrimSpace(noComment))
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return fmt.Errorf("only SELECT (and CTEs starting with WITH ... SELECT) are allowed; got: %.40s", clean)
	}
	// Block any write keywords appearing anywhere in the query.
	blocked := regexp.MustCompile(`\b(INSERT|UPDATE|DELETE|DROP|TRUNCATE|ALTER|CREATE|GRANT|REVOKE|EXECUTE|CALL)\b`)
	if loc := blocked.FindStringIndex(upper); loc != nil {
		return fmt.Errorf("query contains forbidden keyword at position %d", loc[0])
	}
	return nil
}

// appendLimit adds a LIMIT clause to a query if one is not already present.
func appendLimit(sql string, limit int) string {
	upper := strings.ToUpper(sql)
	if strings.Contains(upper, "LIMIT") {
		return sql
	}
	return strings.TrimRight(sql, "; \t\n") + fmt.Sprintf(" LIMIT %d", limit)
}

// resolveMaxRows returns the effective row cap. Respects the configured
// max_rows, defaults to defaultMaxRows, and never exceeds hardCapRows.
func resolveMaxRows(c *connector.Ctx) int {
	n := c.CfgInt("max_rows")
	if n <= 0 {
		n = defaultMaxRows
	}
	if n > hardCapRows {
		n = hardCapRows
	}
	return n
}

// stripLeadingComments removes SQL line comments (--) and block comments
// (/* */) from the start of a query so the verb detection is not fooled.
func stripLeadingComments(sql string) string {
	for {
		s := strings.TrimSpace(sql)
		if strings.HasPrefix(s, "--") {
			if idx := strings.Index(s, "\n"); idx >= 0 {
				sql = s[idx+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(s, "/*") {
			if idx := strings.Index(s, "*/"); idx >= 0 {
				sql = s[idx+2:]
				continue
			}
			return ""
		}
		return s
	}
}
