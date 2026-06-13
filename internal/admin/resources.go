package admin

import (
	"net/http"

	adminview "github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/login"
)

// ── Projects ──────────────────────────────────────────────────────────────────

func (h *Handler) projectsAdminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	if h.projects == nil {
		http.Error(w, "projects not available", http.StatusServiceUnavailable)
		return
	}
	allProjects := h.projects.Projects()
	allTags, _ := h.repo.ListTags(ctx)

	paths := make([]string, 0, len(allProjects))
	for id := range allProjects {
		paths = append(paths, "/projects/"+id)
	}
	perms, _ := h.repo.ListToolPerms(ctx, paths)

	permByPath := make(map[string][]string, len(perms))
	for i, path := range paths {
		permByPath[path] = perms[i].TagIDs
	}

	rows := make([]adminview.ResourceAdminRow, 0, len(allProjects))
	for id, p := range allProjects {
		path := "/projects/" + id
		rows = append(rows, adminview.ResourceAdminRow{
			ID:        id,
			Name:      p.Meta.Name,
			Icon:      p.Meta.Icon,
			CreatedBy: p.Meta.OwnerUserID,
			TagIDs:    permByPath[path],
			Path:      path,
		})
	}

	adminview.ResourcesAdminPage("Projects", "/admin/projects", rows, allTags, user).Render(ctx, w)
}

func (h *Handler) setProjectTags(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	ids := dedupNonEmpty(r.Form["tag_ids[]"])
	if err := h.repo.SetToolTags(r.Context(), "/projects/"+id, ids); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/projects", http.StatusFound)
}

// ── Workflows ─────────────────────────────────────────────────────────────────

func (h *Handler) workflowsAdminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	if h.workflows == nil {
		http.Error(w, "workflows not available", http.StatusServiceUnavailable)
		return
	}
	ids, err := h.workflows.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	allTags, _ := h.repo.ListTags(ctx)

	paths := make([]string, len(ids))
	for i, id := range ids {
		paths[i] = "/workflows/" + id
	}
	perms, _ := h.repo.ListToolPerms(ctx, paths)

	rows := make([]adminview.ResourceAdminRow, len(ids))
	for i, id := range ids {
		rows[i] = adminview.ResourceAdminRow{
			ID:     id,
			Name:   id,
			TagIDs: perms[i].TagIDs,
			Path:   paths[i],
		}
	}

	adminview.ResourcesAdminPage("Workflows", "/admin/workflows", rows, allTags, user).Render(ctx, w)
}

func (h *Handler) setWorkflowTags(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	ids := dedupNonEmpty(r.Form["tag_ids[]"])
	if err := h.repo.SetToolTags(r.Context(), "/workflows/"+id, ids); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/workflows", http.StatusFound)
}

// ── Skills ────────────────────────────────────────────────────────────────────

func (h *Handler) skillsAdminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	if h.skillsDB == nil {
		http.Error(w, "skills not available", http.StatusServiceUnavailable)
		return
	}
	skills, err := h.skillsDB.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	allTags, _ := h.repo.ListTags(ctx)

	paths := make([]string, len(skills))
	for i, sk := range skills {
		paths[i] = "/skills/" + sk.Name
	}
	perms, _ := h.repo.ListToolPerms(ctx, paths)

	rows := make([]adminview.ResourceAdminRow, len(skills))
	for i, sk := range skills {
		createdBy := ""
		if sk.CreatedBy != nil {
			createdBy = *sk.CreatedBy
		}
		rows[i] = adminview.ResourceAdminRow{
			ID:        sk.Name,
			Name:      sk.Name,
			CreatedBy: createdBy,
			TagIDs:    perms[i].TagIDs,
			Path:      paths[i],
		}
	}

	adminview.ResourcesAdminPage("Skills", "/admin/skills", rows, allTags, user).Render(ctx, w)
}

func (h *Handler) setSkillTags(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	r.ParseForm()
	ids := dedupNonEmpty(r.Form["tag_ids[]"])
	if err := h.repo.SetToolTags(r.Context(), "/skills/"+name, ids); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/skills", http.StatusFound)
}
