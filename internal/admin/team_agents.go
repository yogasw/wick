package admin

import (
	"context"
	"net/http"

	adminview "github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/login"
)

// ── Team agents ───────────────────────────────────────────────────────────────

// teamAgentPrefix is the tool_path namespace of a Team agent's share tags.
// Kept equal to team.TagSharePath, which the readers query.
const teamAgentPrefix = "/team-agents/"

// TeamAgent is one Team agent as /admin/team-agents lists it. Block is
// non-empty when the agent can never be shared (the Captain, a remote set
// to "Only me"); its tags are then refused.
type TeamAgent struct {
	ID, Name, Handle, OwnerUserID, Block string
	Disabled                             bool
}

// TeamAgentLister lists every owner's Team agents.
type TeamAgentLister interface {
	TeamAgents(ctx context.Context) ([]TeamAgent, error)
}

// TeamAgentChangeTracker is an optional extra of a TeamAgentLister: it
// tells the agents tool that an agent's share tags are about to change, so
// the rosters that gain or lose it refresh. Call the result once the
// change is saved.
type TeamAgentChangeTracker interface {
	TrackTeamAgentChange(ctx context.Context, id string) func()
}

// SetTeamAgents wires /admin/team-agents. Optional — unset, the page is 503.
func (h *Handler) SetTeamAgents(l TeamAgentLister) { h.teamAgents = l }

// teamAgentsAdminPage shares Team agents by tag. The owner is shown but
// never changed here: an agent carries its owner's Slack channels and
// connector access, so moving it is not a relabel.
func (h *Handler) teamAgentsAdminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	if h.teamAgents == nil {
		http.Error(w, "team agents not available", http.StatusServiceUnavailable)
		return
	}
	all, err := h.teamAgents.TeamAgents(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	agents := all[:0:0]
	for _, a := range all {
		if a.Block == "" {
			agents = append(agents, a)
		}
	}
	allTags, _, tagsOK := h.tagPageData(w, r, nil)
	if !tagsOK {
		return
	}
	ids := make(map[string]struct{}, len(agents))
	paths := make([]string, len(agents))
	for i, a := range agents {
		ids[a.ID] = struct{}{}
		paths[i] = teamAgentPrefix + a.ID
	}
	allTags = filterOwnerTagsForIDs(allTags, ids)
	perms, err := h.repo.ListToolPerms(ctx, paths)
	if err != nil {
		http.Error(w, "cannot load tag assignments: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rows := make([]adminview.ResourceAdminRow, len(agents))
	for i, a := range agents {
		name := a.Name
		if a.Handle != "" && name != a.Handle {
			name += " (@" + a.Handle + ")"
		} else if a.Handle != "" {
			name = "@" + a.Handle
		}
		if a.Disabled {
			name += " · disabled"
		}
		rows[i] = adminview.ResourceAdminRow{
			ID:        a.ID,
			Name:      name,
			CreatedBy: a.OwnerUserID,
			TagIDs:    perms[i].TagIDs,
			Path:      paths[i],
		}
	}
	// No owner picker: see the doc comment above.
	adminview.ResourcesAdminPage("Team agents", "/admin/team-agents", h.decorateResourceRows(ctx, rows, allTags), allTags, adminview.ResourceOwnerEdit{}, user).Render(ctx, w)
}

// setTeamAgentTags saves the share tags of one agent. An unknown agent is
// 404 and one that can never be shared is refused, so no tag row is left
// behind that would start sharing it later.
func (h *Handler) setTeamAgentTags(w http.ResponseWriter, r *http.Request) {
	if h.teamAgents == nil {
		http.Error(w, "team agents not available", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	all, err := h.teamAgents.TeamAgents(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var found *TeamAgent
	for i := range all {
		if all[i].ID == id {
			found = &all[i]
			break
		}
	}
	if found == nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	if found.Block != "" {
		http.Error(w, found.Block, http.StatusConflict)
		return
	}
	ids, ok := tagIDsFromForm(r)
	if !ok {
		refuseUnreadableTagForm(w)
		return
	}
	saved := func() {}
	if t, ok := h.teamAgents.(TeamAgentChangeTracker); ok {
		saved = t.TrackTeamAgentChange(r.Context(), id)
	}
	if err := h.repo.SetToolTags(r.Context(), teamAgentPrefix+id, ids); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	saved()
	redirectOrNoContent(w, r, "/admin/team-agents")
}

// teamAgentOwner is the owner of the agent at /team-agents/<id>, for the
// access modal; "" when unknown.
func (h *Handler) teamAgentOwner(id string) string {
	if h.teamAgents == nil {
		return ""
	}
	all, err := h.teamAgents.TeamAgents(context.Background())
	if err != nil {
		return ""
	}
	for _, a := range all {
		if a.ID == id {
			return a.OwnerUserID
		}
	}
	return ""
}
