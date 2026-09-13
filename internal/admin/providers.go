package admin

import (
	"net/http"
	"strings"

	adminview "github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/login"
)

// Provider sharing, admin side.
//
// A provider instance is not a database row — it lives in the user config
// file — so there is nothing to hang a foreign key on. It does not need
// one: tags already attach to a PATH in tool_tags, which is how
// connectors, projects, skills and data tables are shared. Providers join
// that scheme with two paths per instance:
//
//	/providers/<type>/<name>         → ACCESS   (empty = everyone)
//	/providers/<type>/<name>/manage  → MANAGE   (empty = admins only)
//
// The two must stay in sync with the readers in
// internal/tools/agents/provider_access.go — same strings, opposite
// defaults.

// providerAccessTagPath returns the tool_tags path carrying an
// instance's ACCESS tags.
func providerAccessTagPath(typ, name string) string {
	return "/providers/" + typ + "/" + name
}

// providerManageTagPath returns the tool_tags path carrying an
// instance's MANAGE tags.
func providerManageTagPath(typ, name string) string {
	return providerAccessTagPath(typ, name) + "/manage"
}

// providersAdminPage renders the provider sharing table.
func (h *Handler) providersAdminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)

	instances, err := provider.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	accessPaths := make([]string, len(instances))
	managePaths := make([]string, len(instances))
	for i, ins := range instances {
		accessPaths[i] = providerAccessTagPath(string(ins.Type), ins.Name)
		managePaths[i] = providerManageTagPath(string(ins.Type), ins.Name)
	}
	// Both lookups must succeed: a row rendered with its tags missing
	// reads as "untagged", and saving it would make that true.
	allTags, accessPerms, ok := h.tagPageData(w, r, accessPaths)
	if !ok {
		return
	}
	managePerms, err := h.repo.ListToolPerms(ctx, managePaths)
	if err != nil {
		http.Error(w, "cannot load manage tags: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rows := make([]adminview.ProviderAdminRow, len(instances))
	for i, ins := range instances {
		row := adminview.ProviderAdminRow{
			Type:     string(ins.Type),
			Name:     ins.Name,
			Binary:   ins.Binary,
			Disabled: ins.Disabled,
			Path:     string(ins.Type) + "/" + ins.Name,
		}
		if i < len(accessPerms) && accessPerms[i] != nil {
			row.AccessTagIDs = accessPerms[i].TagIDs
		}
		if i < len(managePerms) && managePerms[i] != nil {
			row.ManageTagIDs = managePerms[i].TagIDs
		}
		rows[i] = row
	}

	adminview.ProvidersAdminPage(rows, allTags, user).Render(ctx, w)
}

// setProviderAccessTags writes the ACCESS tags of one instance.
func (h *Handler) setProviderAccessTags(w http.ResponseWriter, r *http.Request) {
	h.setProviderTags(w, r, providerAccessTagPath)
}

// setProviderManageTags writes the MANAGE tags of one instance.
func (h *Handler) setProviderManageTags(w http.ResponseWriter, r *http.Request) {
	h.setProviderTags(w, r, providerManageTagPath)
}

// setProviderTags is the shared body of both writers.
//
// The instance is resolved before writing: a path segment straight from
// the URL would otherwise let anyone create tag rows for providers that
// do not exist, which is harmless but leaves rubbish that outlives the
// typo.
func (h *Handler) setProviderTags(w http.ResponseWriter, r *http.Request, path func(typ, name string) string) {
	typ := strings.TrimSpace(r.PathValue("type"))
	name := strings.TrimSpace(r.PathValue("name"))
	if _, err := provider.Find(provider.Type(typ), name); err != nil {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	ids, ok := tagIDsFromForm(r)
	if !ok {
		refuseUnreadableTagForm(w)
		return
	}
	if err := h.repo.SetToolTags(r.Context(), path(typ, name), ids); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectOrNoContent(w, r, "/admin/providers")
}
