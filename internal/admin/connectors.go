package admin

import (
	"context"
	"net/http"

	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
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
	h.repo.ResolveOwnerDisplayNames(ctx, allTags)
	connectorIDs := make(map[string]struct{}, len(rows))
	for _, c := range rows {
		connectorIDs[c.ID] = struct{}{}
	}
	allTags = filterOwnerTagsForIDs(allTags, connectorIDs)

	paths := make([]string, len(rows))
	for i, c := range rows {
		paths[i] = "/connectors/" + c.ID
	}
	perms, _ := h.repo.ListToolPerms(ctx, paths)

	accountsByRow, accountPerms, ownerLabels := h.connectorAccountsAdmin(ctx, rows)

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
		for _, acc := range accountsByRow[c.ID] {
			row.Accounts = append(row.Accounts, view.ConnectorAccountAdminRow{
				Account:    acc,
				OwnerLabel: ownerLabels[acc.WickUserID],
				TagIDs:     accountPerms[connectors.AccountTagPath(acc.ID)],
			})
		}
		items[i] = row
	}

	view.ConnectorsAdminPage(items, allTags, user).Render(ctx, w)
}

// connectorAccountsAdmin loads the connected OAuth accounts of every listed
// instance (one lookup per row), then resolves their access tags (tool_tags
// on AccountTagPath) and their owners' display labels in one batched query
// each. Returns accounts keyed by connector id, tag ids keyed by tool path,
// and owner labels keyed by wick user id.
func (h *Handler) connectorAccountsAdmin(ctx context.Context, rows []entity.Connector) (map[string][]entity.ConnectorAccount, map[string][]string, map[string]string) {
	byRow := make(map[string][]entity.ConnectorAccount, len(rows))
	paths := []string{}
	ownerIDs := []string{}
	for _, c := range rows {
		accs, err := h.connectors.ListAccounts(ctx, c.ID)
		if err != nil || len(accs) == 0 {
			continue
		}
		byRow[c.ID] = accs
		for _, acc := range accs {
			paths = append(paths, connectors.AccountTagPath(acc.ID))
			if acc.WickUserID != "" {
				ownerIDs = append(ownerIDs, acc.WickUserID)
			}
		}
	}
	tagsByPath := map[string][]string{}
	if len(paths) > 0 {
		if perms, err := h.repo.ListToolPerms(ctx, paths); err == nil {
			for _, p := range perms {
				tagsByPath[p.Path] = p.TagIDs
			}
		}
	}
	return byRow, tagsByPath, h.repo.UserLabels(ctx, ownerIDs)
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
	redirectOrNoContent(w, r, "/admin/connectors")
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
	redirectOrNoContent(w, r, "/admin/connectors")
}

// setConnectorAccountTagsAdmin updates the access tags of ONE connected
// OAuth account. Same ToolTag table as everything else, on the account path
// (connectors.AccountTagPath) — so an admin can share a single connected
// identity with a team while the rest of the instance's accounts stay
// private to whoever connected them.
func (h *Handler) setConnectorAccountTagsAdmin(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("accountID")
	if _, err := h.connectors.GetAccount(r.Context(), accountID); err != nil {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}
	r.ParseForm()
	ids := dedupNonEmpty(r.Form["tag_ids[]"])
	if err := h.repo.SetToolTags(r.Context(), connectors.AccountTagPath(accountID), ids); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectOrNoContent(w, r, "/admin/connectors")
}
