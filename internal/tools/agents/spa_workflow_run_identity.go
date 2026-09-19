package agents

import (
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/tool"
)

// run-identity answers the one question the workspace picker cannot answer
// locally: whose access a run actually borrows.
//
// A workflow's agent sessions are attached to the workflow's OWNER
// (session_init → EnsureSessionOwner with Workflow.CreatedBy). So a workspace
// that the person editing can reach is not the same as one the run can reach,
// and an owner-less workflow borrows no identity at all — its spawns fall back
// to the internal principal, which carries no tags and therefore no MCP.
//
// None of that is visible in the editor, where the picker just shows a project
// name. This endpoint gives it the three facts it needs to say so out loud.

func registerSPAWorkflowRunIdentity(r tool.Router) {
	r.GET("/api/workflows/run-identity/{id}", spaWorkflowRunIdentity)
}

type runIdentityResponse struct {
	// OwnerID is who the workflow belongs to; empty means nobody does.
	OwnerID string `json:"owner_id"`
	// OwnerLabel is that person's display name, or "" when the account is
	// gone — an owner id naming no user is itself worth surfacing.
	OwnerLabel string `json:"owner_label"`
	// OwnerKnown is false when OwnerID names an account that no longer
	// resolves, which reads very differently from having no owner at all.
	OwnerKnown bool `json:"owner_known"`
	// ProjectID echoes the project asked about ("" when none was).
	ProjectID string `json:"project_id"`
	// ProjectName is its display name; ProjectExists says whether the id
	// still resolves to a project at all.
	ProjectName   string `json:"project_name"`
	ProjectExists bool   `json:"project_exists"`
	// OwnerAccess: can the identity the run borrows reach that project?
	OwnerAccess bool `json:"owner_access"`
	// ViewerAccess: can the person looking at the editor reach it? False
	// here is not an error — it is why the project may be missing from
	// their own picker.
	ViewerAccess bool `json:"viewer_access"`
}

func spaWorkflowRunIdentity(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	w, err := globalWorkflowMgr.Service.LoadDraft(id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	resp := runIdentityResponse{
		OwnerID:   w.CreatedBy,
		ProjectID: strings.TrimSpace(c.Query("project")),
	}
	if resp.OwnerID != "" && globalAuth != nil {
		if u, uerr := globalAuth.GetUserByID(c.Context(), resp.OwnerID); uerr == nil && u != nil {
			resp.OwnerKnown = true
			resp.OwnerLabel = strings.TrimSpace(u.Name)
			if resp.OwnerLabel == "" {
				resp.OwnerLabel = u.Email
			}
		}
	}
	if resp.ProjectID == "" {
		c.JSON(http.StatusOK, resp)
		return
	}

	if p, found := globalMgr.Registry().Projects()[resp.ProjectID]; found {
		resp.ProjectExists = true
		resp.ProjectName = p.Meta.Name
	}
	resp.ViewerAccess = callerProjectAccess(c).allowProject(resp.ProjectID)
	if resp.OwnerID != "" && globalAuth != nil {
		// Asked about the OWNER, inside the viewer's request — the access
		// check has to be resolved from that user, not from this session.
		if u, uerr := globalAuth.GetUserByID(c.Context(), resp.OwnerID); uerr == nil && u != nil {
			resp.OwnerAccess = projectAccessForUser(c, u).allowProject(resp.ProjectID)
		}
	}
	c.JSON(http.StatusOK, resp)
}
