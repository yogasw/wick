package connectors

import (
	"context"
	"strings"
	"testing"
)

// roleTestScope is an AgentScope whose RoleScope answers from a table.
type roleTestScope struct {
	levels map[string]string // caller|connector|lent -> level
	ops    []string
}

func (roleTestScope) AllowConnector(string) bool        { return true }
func (roleTestScope) AllowAccount(string, string) bool  { return true }
func (roleTestScope) AllowOp(string, string, bool) bool { return true }
func (r roleTestScope) CallerLevel(_ context.Context, caller, id string, lent bool) (string, []string, string) {
	k := caller + "|" + id + "|"
	if lent {
		k += "lent"
	}
	lv, ok := r.levels[k]
	if !ok {
		return "off", nil, ""
	}
	return lv, r.ops, ""
}

func TestRoleAllowsOp(t *testing.T) {
	ctx := WithAgentScope(context.Background(), roleTestScope{levels: map[string]string{
		"a|docs|lent": "read", "a|docs|": "all", "b|docs|lent": "pick",
	}, ops: []string{"get"}})
	cases := []struct {
		caller, op  string
		destructive bool
		lent, ok    bool
	}{
		{"a", "get", false, true, true},
		{"a", "delete", true, true, false}, // read on a lend: no writes
		{"a", "delete", true, false, true}, // own access: untouched
		{"b", "get", false, true, true},
		{"b", "list", false, true, false}, // pick: only the listed ops
		{"g", "get", false, false, false}, // no level: off
	}
	for _, c := range cases {
		err := roleAllowsOp(ctx, c.caller, "docs", c.op, c.destructive, c.lent)
		if (err == nil) != c.ok {
			t.Errorf("roleAllowsOp(%s, %s, lent=%v) = %v, want ok=%v", c.caller, c.op, c.lent, err, c.ok)
		}
		if err != nil && !strings.Contains(err.Error(), c.op) {
			t.Errorf("error %q does not name the op", err)
		}
	}
	// No RoleScope on ctx: nothing is limited.
	if err := roleAllowsOp(context.Background(), "", "docs", "delete", true, true); err != nil {
		t.Fatalf("no role scope refused: %v", err)
	}
}
