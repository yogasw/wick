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
	h.repo.ResolveOwnerDisplayNames(ctx, allTags)
	projectIDs := make(map[string]struct{}, len(allProjects))
	for id := range allProjects {
		projectIDs[id] = struct{}{}
	}
	allTags = filterOwnerTagsForIDs(allTags, projectIDs)

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
	h.repo.ResolveOwnerDisplayNames(ctx, allTags)
	workflowIDs := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		workflowIDs[id] = struct{}{}
	}
	allTags = filterOwnerTagsForIDs(allTags, workflowIDs)

	paths := make([]string, len(ids))
	for i, id := range ids {
		paths[i] = "/workflows/" + id
	}
	perms, _ := h.repo.ListToolPerms(ctx, paths)

	rows := make([]adminview.ResourceAdminRow, len(ids))
	for i, id := range ids {
		row := adminview.ResourceAdminRow{
			ID:     id,
			Name:   id,
			TagIDs: perms[i].TagIDs,
			Path:   paths[i],
		}
		if info, err := h.workflows.LoadInfo(id); err == nil {
			if info.Name != "" {
				row.Name = info.Name
			}
			row.CreatedBy = info.CreatedBy
		}
		rows[i] = row
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
	h.repo.ResolveOwnerDisplayNames(ctx, allTags)
	skillIDs := make(map[string]struct{}, len(skills))
	for _, sk := range skills {
		skillIDs[sk.Name] = struct{}{}
	}
	allTags = filterOwnerTagsForIDs(allTags, skillIDs)

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

// ── Data Tables ───────────────────────────────────────────────────────────────

func (h *Handler) dataTablesAdminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	if h.dataTables == nil {
		http.Error(w, "data tables not available", http.StatusServiceUnavailable)
		return
	}
	slugs := h.dataTables.ListTables()
	allTags, _ := h.repo.ListTags(ctx)
	h.repo.ResolveOwnerDisplayNames(ctx, allTags)
	tableIDs := make(map[string]struct{}, len(slugs))
	for _, s := range slugs {
		tableIDs[s] = struct{}{}
	}
	allTags = filterOwnerTagsForIDs(allTags, tableIDs)

	paths := make([]string, len(slugs))
	for i, s := range slugs {
		paths[i] = "/data-tables/" + s
	}
	perms, _ := h.repo.ListToolPerms(ctx, paths)

	rows := make([]adminview.ResourceAdminRow, len(slugs))
	for i, slug := range slugs {
		row := adminview.ResourceAdminRow{
			ID:     slug,
			Name:   slug,
			TagIDs: perms[i].TagIDs,
			Path:   paths[i],
		}
		if sc, err := h.dataTables.LoadSchema(slug); err == nil {
			if sc.Name != "" {
				row.Name = sc.Name
			}
			row.CreatedBy = sc.UserID
		}
		rows[i] = row
	}

	adminview.ResourcesAdminPage("Data Tables", "/admin/data-tables", rows, allTags, user).Render(ctx, w)
}

func (h *Handler) setDataTableTags(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	r.ParseForm()
	ids := dedupNonEmpty(r.Form["tag_ids[]"])
	if err := h.repo.SetToolTags(r.Context(), "/data-tables/"+slug, ids); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/data-tables", http.StatusFound)
}
