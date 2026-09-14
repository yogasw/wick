package api

import (
	"context"

	"github.com/yogasw/wick/internal/mcp"
	"github.com/yogasw/wick/internal/tags"
)

// workflowOwnership records the human a workflow belongs to when an agent
// creates one through the MCP workflow connector, using the same owner:<id>
// tag the /workflows UI writes on its own create paths.
//
// Without it an agent-created workflow landed ownerless, and the listing
// rule (owner or admin) then hid it from the very person who asked for it —
// so the only way to let them edit it was to make them an admin. Implements
// wfconn.WorkflowOwnership.
//
// The caller id is the SESSION owner, resolved upstream in
// connectors.Service. A system/internal principal records nothing, which
// leaves such a workflow admin-only rather than owned by a synthetic
// account nobody can log in as.
type workflowOwnership struct {
	tags *tags.Service
}

// RegisterOwner links userID to the workflow via its owner tag.
func (o workflowOwnership) RegisterOwner(ctx context.Context, userID, workflowID string) {
	if o.tags == nil || userID == "" || userID == mcp.InternalAgentUserID || workflowID == "" {
		return
	}
	_ = o.tags.CreateResourceOwnerTag(ctx, workflowID, userID)
}
