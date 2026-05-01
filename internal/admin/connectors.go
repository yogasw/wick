package admin

import (
	"net/http"

	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/login"
)

// connectorsAdminPage lists every Connector row across all registered
// definitions plus any orphaned rows whose Key has lost its module.
// Disabled toggle and tag picker reuse the existing ToolPermission /
// ToolTag tables, addressed by path "/connectors/{id}".
func (h *Handler) connectorsAdminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)

	rows, err := h.connectors.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	allTags, _ := h.repo.ListTags(ctx)

	paths := make([]string, len(rows))
	for i, c := range rows {
		paths[i] = "/connectors/" + c.ID
	}
	perms, _ := h.repo.ListToolPerms(ctx, paths)

	items := make([]view.ConnectorAdminRow, len(rows))
	for i, c := range rows {
		mod, ok := h.connectors.Module(c.Key)
		row := view.ConnectorAdminRow{
			Connector:     c,
			ModuleMissing: !ok,
			TagIDs:        perms[i].TagIDs,
		}
		if ok {
			row.ModuleName = mod.Meta.Name
			row.ModuleIcon = mod.Meta.Icon
		}
		items[i] = row
	}

	view.ConnectorsAdminPage(items, allTags, user).Render(ctx, w)
}

// setConnectorDisabledAdmin toggles the row-level Disabled flag on the
// Connector entity itself (NOT the ToolPermission row). Disabled rows
// disappear from MCP tools/list and the Postman-style test panel.
func (h *Handler) setConnectorDisabledAdmin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	disabled := boolParam(r, "disabled")
	if err := h.connectors.SetDisabled(r.Context(), id, disabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/connectors", http.StatusFound)
}

// setConnectorTagsAdmin updates the access tags for one connector
// instance. Reuses the ToolTag table with path "/connectors/{id}" so
// the same tag-filter rules apply to MCP and the manager surface.
func (h *Handler) setConnectorTagsAdmin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	ids := dedupNonEmpty(r.Form["tag_ids[]"])
	if err := h.repo.SetToolTags(r.Context(), "/connectors/"+id, ids); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/connectors", http.StatusFound)
}
