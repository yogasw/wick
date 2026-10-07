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

	accountsByRow, accountPerms, ownerLabels, err := h.connectorAccountsAdmin(ctx, rows)
	if err != nil {
		http.Error(w, "cannot load connected accounts: "+err.Error(), http.StatusInternalServerError)
		return
	}

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

	// Reach badges: the instance row and every account under it are gated
	// separately, so both get their own count.
	accessPaths := make([]string, 0, len(paths))
	accessPaths = append(accessPaths, paths...)
	for i := range items {
		for _, acc := range items[i].Accounts {
			accessPaths = append(accessPaths, connectors.AccountTagPath(acc.Account.ID))
		}
	}
	access := h.accessSummaries(ctx, accessPaths)
	// One batch for every account on the page: resolving each account on its
	// own was ~6 round trips each, which on a remote database is seconds.
	batch := h.newAccountReachBatch(ctx, accessPaths)
	for i := range items {
		items[i].Access = access["/connectors/"+items[i].Connector.ID]
		items[i].TagNames = view.TagNames(allTags, items[i].TagIDs)
		for j := range items[i].Accounts {
			acc := &items[i].Accounts[j]
			// An account's reach is wider than its tags (owner, creator,
			// admins, shared pool), so it is counted on its own terms.
			acc.Access = batch.summaryFor(items[i].Connector, acc.Account)
			acc.TagNames = view.TagNames(allTags, acc.TagIDs)
		}
	}

	// The Owner column names a person. ownerLabels only covers the users who
	// connected an ACCOUNT, so instance owners get their own lookup — one for
	// the page, not one per row.
	names := h.userLabels(ctx)
	for i := range items {
		items[i].OwnerLabel = labelFor(names, items[i].Connector.CreatedBy)
	}

	view.ConnectorsAdminPage(items, allTags, h.userOptions(ctx), user).Render(ctx, w)
}

// connectorAccountsAdmin loads the connected OAuth accounts of every listed
// instance (one lookup per row), then resolves their access tags (tool_tags
// on AccountTagPath) and their owners' display labels in one batched query
// each. Returns accounts keyed by connector id, tag ids keyed by tool path,
// and owner labels keyed by wick user id.
func (h *Handler) connectorAccountsAdmin(ctx context.Context, rows []entity.Connector) (map[string][]entity.ConnectorAccount, map[string][]string, map[string]string, error) {
	ids := make([]string, 0, len(rows))
	for _, c := range rows {
		ids = append(ids, c.ID)
	}
	// One query for every instance's accounts. Asking per row was a round
	// trip each, and this page lists every instance on the install.
	byRow, err := h.connectors.ListAccountsFor(ctx, ids)
	if err != nil {
		// Swallowing this would render the page with accounts missing and
		// nothing saying so — on the one screen whose job is showing who can
		// reach which account, a silently short list is worse than an error.
		return nil, nil, nil, err
	}
	paths := []string{}
	ownerIDs := []string{}
	for _, c := range rows {
		for _, acc := range byRow[c.ID] {
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
	return byRow, tagsByPath, h.repo.UserLabels(ctx, ownerIDs), nil
}

// Turning an instance on or off is NOT here. It used to be a second toggle on
// this page, which meant two screens could disable a connector and only one of
// them (the instance settings page, /manager/connectors/{key}/{id}) shows what
// else is about to break when you do. This page grants and describes access;
// the settings page owns the switch.

// setConnectorTagsAdmin updates the access tags for one connector
// instance. Reuses the ToolTag table with path "/connectors/{id}" so
// the same tag-filter rules apply to MCP and the manager surface.
func (h *Handler) setConnectorTagsAdmin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ids, ok := tagIDsFromForm(r)
	if !ok {
		refuseUnreadableTagForm(w)
		return
	}
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
	ids, ok := tagIDsFromForm(r)
	if !ok {
		refuseUnreadableTagForm(w)
		return
	}
	if err := h.repo.SetToolTags(r.Context(), connectors.AccountTagPath(accountID), ids); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectOrNoContent(w, r, "/admin/connectors")
}
