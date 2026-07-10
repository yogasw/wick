package api

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow/datatable"
	"github.com/yogasw/wick/internal/mcp"
)

// TestDataTableACL_CanAccess pins the MCP/agent gate: the resolved session
// owner reaches only their own tables; the internal/system principal and an
// empty caller are unrestricted; other users are denied. tags/login/cfg are
// nil here so only the system shortcuts + direct-owner fallback are exercised
// (the owner-tag path is covered by the tags service's own tests).
func TestDataTableACL_CanAccess(t *testing.T) {
	mock := datatable.NewMock()
	if err := mock.CreateTable(datatable.Schema{
		Slug:    "tasks",
		UserID:  "alice",
		Columns: []datatable.Column{{Name: "title", Type: "string"}},
	}); err != nil {
		t.Fatalf("seed table: %v", err)
	}
	acl := dataTableACL{dt: mock}
	ctx := context.Background()

	cases := []struct {
		name   string
		userID string
		want   bool
	}{
		{"owner reaches own table (direct fallback)", "alice", true},
		{"other user is denied", "bob", false},
		{"empty caller is unrestricted", "", true},
		{"internal agent principal is unrestricted", mcp.InternalAgentUserID, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := acl.CanAccess(ctx, tc.userID, "tasks"); got != tc.want {
				t.Fatalf("CanAccess(%q) = %v, want %v", tc.userID, got, tc.want)
			}
		})
	}
}
