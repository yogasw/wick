package api

import (
	"context"

	"github.com/rs/zerolog/log"

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
//
// A failure is logged, not returned: the workflow itself was created, and
// failing the whole call because the owner tag did not stick would throw
// away work the caller already has. But it must not be silent either — the
// visible symptom is a workflow missing from its own author's list, which
// looks like a listing bug and sends anyone debugging it to the wrong file.
func (o workflowOwnership) RegisterOwner(ctx context.Context, userID, workflowID string) {
	if o.tags == nil || userID == "" || userID == mcp.InternalAgentUserID || workflowID == "" {
		return
	}
	if err := o.tags.CreateResourceOwnerTag(ctx, workflowID, userID); err != nil {
		log.Warn().Err(err).
			Str("workflow", workflowID).
			Str("user", userID).
			Msg("workflow ownership: owner tag not recorded; the workflow stays admin-only")
	}
}
