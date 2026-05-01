package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/accesstoken"
	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/oauth"
	"github.com/yogasw/wick/internal/sso"
	"github.com/yogasw/wick/pkg/tool"

	"gorm.io/gorm"
)

// JobListProvider is the subset of manager.Service that the admin panel needs.
type JobListProvider interface {
	ListJobs(ctx context.Context) ([]entity.Job, error)
}

type Handler struct {
	repo       *repo
	tools      []tool.Tool
	configs    *configs.Service
	sso        *sso.Service
	svc        JobListProvider
	connectors *connectors.Service
	tokens     *accesstoken.Service
	oauth      *oauth.Service
}

func NewHandler(
	db *gorm.DB,
	tools []tool.Tool,
	configsSvc *configs.Service,
	ssoSvc *sso.Service,
	svc JobListProvider,
	connectorsSvc *connectors.Service,
	tokensSvc *accesstoken.Service,
	oauthSvc *oauth.Service,
) *Handler {
	return &Handler{
		repo:       newRepo(db),
		tools:      tools,
		configs:    configsSvc,
		sso:        ssoSvc,
		svc:        svc,
		connectors: connectorsSvc,
		tokens:     tokensSvc,
		oauth:      oauthSvc,
	}
}

func (h *Handler) Register(mux *http.ServeMux, sessionMidd *login.Middleware) {
	admin := func(next http.HandlerFunc) http.Handler {
		return sessionMidd.RequireAuth(sessionMidd.RequireAdmin(next))
	}
	redirect := func(to string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, to, http.StatusFound)
		}
	}

	// Dashboard is the admin landing page.
	mux.Handle("GET /admin", admin(h.dashboardPage))

	// User management
	mux.Handle("GET /admin/users", admin(h.usersPage))

	// Tool permissions (visibility, enable/disable, tags)
	mux.Handle("GET /admin/tools", admin(h.toolsPage))

	// Tool detail pages now live in Manager — redirect old URLs.
	mux.Handle("GET /admin/tools/{key}", admin(func(w http.ResponseWriter, r *http.Request) {
		redirect("/manager/tools/" + r.PathValue("key"))(w, r)
	}))

	// Jobs admin page (visibility, tags — schedule/run lives in Manager)
	mux.Handle("GET /admin/jobs", admin(h.adminJobsPage))
	mux.Handle("GET /admin/jobs/{key}", admin(func(w http.ResponseWriter, r *http.Request) {
		redirect("/manager/jobs/" + r.PathValue("key"))(w, r)
	}))

	// Job actions
	mux.Handle("POST /admin/jobs/{path}/disabled", admin(h.setJobDisabled))
	mux.Handle("POST /admin/jobs/{path}/tags", admin(h.setJobTags))

	mux.Handle("GET /admin/tags", admin(h.tagsPage))
	mux.Handle("GET /admin/configs", admin(h.configsHubPage))
	mux.Handle("GET /admin/configs/sso", admin(h.ssoPage))
	mux.Handle("POST /admin/configs/sso/{provider}", admin(h.updateSSO))

	// Variables (app-level configs)
	mux.Handle("GET /admin/variables", admin(h.variablesPage))
	mux.Handle("POST /admin/variables/{key}", admin(h.setVariable))
	mux.Handle("POST /admin/variables/{key}/regenerate", admin(h.regenerateVariable))

	// User actions
	mux.Handle("POST /admin/users/{id}/approve", admin(h.approveUser))
	mux.Handle("POST /admin/users/{id}/unapprove", admin(h.unapproveUser))
	mux.Handle("POST /admin/users/{id}/role", admin(h.setRole))
	mux.Handle("POST /admin/users/{id}/tags", admin(h.setUserTags))

	// Tool actions (admin-only permission management)
	mux.Handle("POST /admin/tools/{path}/visibility", admin(h.setToolVisibility))
	mux.Handle("POST /admin/tools/{path}/tags", admin(h.setToolTags))
	mux.Handle("POST /admin/tools/{path}/disabled", admin(h.setToolDisabled))

	// Tag CRUD
	mux.Handle("GET /admin/tags.json", admin(h.listTagsJSON))
	mux.Handle("POST /admin/tags", admin(h.createTag))
	mux.Handle("POST /admin/tags/{id}/update", admin(h.updateTag))
	mux.Handle("POST /admin/tags/{id}/delete", admin(h.deleteTag))

	// Connector instance management (cross-definition list).
	mux.Handle("GET /admin/connectors", admin(h.connectorsAdminPage))
	mux.Handle("POST /admin/connectors/{id}/disabled", admin(h.setConnectorDisabledAdmin))
	mux.Handle("POST /admin/connectors/{id}/tags", admin(h.setConnectorTagsAdmin))

	// Personal access tokens (admin override view). PATs authenticate
	// any wick HTTP API — MCP is just one caller — so the surface is
	// /admin/access-tokens, not /admin/mcp.
	mux.Handle("GET /admin/access-tokens", admin(h.accessTokensAdminPage))
	mux.Handle("POST /admin/access-tokens/{id}/revoke", admin(h.revokeTokenAdmin))

	// OAuth connected apps (admin override view).
	mux.Handle("GET /admin/connections", admin(h.connectionsAdminPage))
	mux.Handle("POST /admin/connections/{userID}/{clientID}/disconnect", admin(h.disconnectGrantAdmin))
}

// ── Dashboard ──────────────────────────────────────────────────

func (h *Handler) dashboardPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)

	jobs, _ := h.svc.ListJobs(ctx)
	running := 0
	enabled := 0
	for _, j := range jobs {
		if j.Enabled {
			enabled++
		}
		if j.LastStatus == entity.JobStatusRunning {
			running++
		}
	}

	totalConfigs, missingTotal, entries := h.gatherMissing(ctx, jobs)

	conns, _ := h.connectors.List(ctx)
	disabledConns := 0
	for _, c := range conns {
		if c.Disabled {
			disabledConns++
		}
	}
	tokens, _ := h.tokens.ListAllActive(ctx)
	grants, _ := h.oauth.ListAllGrants(ctx)

	stats := view.DashboardStats{
		TotalJobs:          len(jobs),
		EnabledJobs:        enabled,
		RunningJobs:        running,
		TotalTools:         h.countTools(),
		TotalConfigs:       totalConfigs,
		MissingConfigs:     missingTotal,
		TotalConnectors:    len(conns),
		DisabledConnectors: disabledConns,
		ActiveTokens:       len(tokens),
		ConnectedApps:      len(grants),
	}
	view.DashboardPage(stats, entries, user).Render(ctx, w)
}

func (h *Handler) countTools() int {
	n := 0
	for _, t := range h.tools {
		if strings.HasPrefix(t.Path, "/tools/") {
			n++
		}
	}
	return n
}

func (h *Handler) gatherMissing(ctx context.Context, jobs []entity.Job) (total int, missing int, entries []view.MissingEntry) {
	// App-level variables
	vars := h.configs.ListOwned("")
	total += len(vars)
	if keys := requiredMissingKeys(vars); len(keys) > 0 {
		entries = append(entries, view.MissingEntry{
			Scope:   "variables",
			Name:    "App variables",
			Icon:    "🔧",
			URL:     "/admin/variables",
			Missing: keys,
		})
		missing += len(keys)
	}

	// Tools — link to manager for config editing
	for _, t := range h.tools {
		if !strings.HasPrefix(t.Path, "/tools/") {
			continue
		}
		rows := h.configs.ListOwned(t.Key)
		total += len(rows)
		if keys := requiredMissingKeys(rows); len(keys) > 0 {
			entries = append(entries, view.MissingEntry{
				Scope:   "tool",
				Key:     t.Key,
				Name:    t.Name,
				Icon:    t.Icon,
				URL:     "/manager/tools/" + t.Key,
				Missing: keys,
			})
			missing += len(keys)
		}
	}

	// Jobs — link to manager
	for _, j := range jobs {
		rows := h.configs.ListOwned(j.Key)
		total += len(rows)
		if keys := requiredMissingKeys(rows); len(keys) > 0 {
			entries = append(entries, view.MissingEntry{
				Scope:   "job",
				Key:     j.Key,
				Name:    j.Name,
				Icon:    j.Icon,
				URL:     "/manager/jobs/" + j.Key,
				Missing: keys,
			})
			missing += len(keys)
		}
	}
	return
}

func requiredMissingKeys(rows []entity.Config) []string {
	var out []string
	for _, v := range rows {
		if v.Required && v.Value == "" {
			out = append(out, v.Key)
		}
	}
	return out
}

// ── Variables ──────────────────────────────────────────────────

func (h *Handler) variablesPage(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	editKey := r.URL.Query().Get("edit")
	view.VariablesPage(h.configs.List(), editKey, user).Render(r.Context(), w)
}

func (h *Handler) setVariable(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	value := r.FormValue("value")
	if err := h.configs.Set(r.Context(), key, value); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/variables", http.StatusFound)
}

func (h *Handler) regenerateVariable(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if err := h.configs.Regenerate(r.Context(), key); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/variables", http.StatusFound)
}

// ── Page handlers ──────────────────────────────────────────────

func (h *Handler) usersPage(w http.ResponseWriter, r *http.Request) {
	currentUser := login.GetUser(r.Context())
	users, err := h.repo.ListUsers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	allTags, _ := h.repo.ListTags(r.Context())
	adminCount, _ := h.repo.CountAdmins(r.Context())
	items := make([]view.UserRow, len(users))
	for i, u := range users {
		ids, _ := h.repo.GetUserTagIDs(r.Context(), u.ID)
		items[i] = view.UserRow{User: u, TagIDs: ids}
	}
	view.UsersPage(items, allTags, currentUser, int(adminCount)).Render(r.Context(), w)
}

func (h *Handler) toolsPage(w http.ResponseWriter, r *http.Request) {
	currentUser := login.GetUser(r.Context())

	var toolsOnly []tool.Tool
	for _, t := range h.tools {
		if strings.HasPrefix(t.Path, "/tools/") {
			toolsOnly = append(toolsOnly, t)
		}
	}

	paths := make([]string, len(toolsOnly))
	for i, t := range toolsOnly {
		paths[i] = t.Path
	}
	perms, _ := h.repo.ListToolPerms(r.Context(), paths)
	allTags, _ := h.repo.ListTags(r.Context())

	items := make([]view.ToolRow, len(toolsOnly))
	for i, t := range toolsOnly {
		items[i] = view.ToolRow{
			Tool:        t,
			Visibility:  perms[i].Visibility,
			Disabled:    perms[i].Disabled,
			TagIDs:      perms[i].TagIDs,
			ConfigCount: len(h.configs.ListOwned(t.Key)),
		}
	}
	view.ToolsPage(items, allTags, currentUser).Render(r.Context(), w)
}

func (h *Handler) tagsPage(w http.ResponseWriter, r *http.Request) {
	currentUser := login.GetUser(r.Context())
	tags, err := h.repo.ListTags(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	editID := r.URL.Query().Get("edit")
	view.TagsPage(tags, currentUser, editID).Render(r.Context(), w)
}

// ── User action handlers ───────────────────────────────────────

func (h *Handler) approveUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.repo.SetApproved(r.Context(), id, true); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

func (h *Handler) unapproveUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.repo.SetApproved(r.Context(), id, false); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

func (h *Handler) setRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role := entity.UserRole(r.FormValue("role"))
	if role != entity.RoleAdmin && role != entity.RoleUser {
		http.Error(w, "invalid role", http.StatusBadRequest)
		return
	}
	if err := h.repo.SetRole(r.Context(), id, role); err != nil {
		if errors.Is(err, ErrLastAdmin) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

func (h *Handler) setUserTags(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	ids := dedupNonEmpty(r.Form["tag_ids[]"])
	if err := h.repo.SetUserTags(r.Context(), id, ids); err != nil {
		if errors.Is(err, ErrUserNotApproved) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

// ── Job page handler ──────────────────────────────────────────

func (h *Handler) adminJobsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	jobs, err := h.svc.ListJobs(ctx)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	paths := make([]string, len(jobs))
	for i, j := range jobs {
		paths[i] = "/jobs/" + j.Key
	}
	perms, _ := h.repo.ListToolPerms(ctx, paths)
	allTags, _ := h.repo.ListTags(ctx)

	rows := make([]view.JobRow, len(jobs))
	for i, j := range jobs {
		rows[i] = view.JobRow{
			Job:         j,
			Disabled:    perms[i].Disabled,
			TagIDs:      perms[i].TagIDs,
			ConfigCount: len(h.configs.ListOwned(j.Key)),
		}
	}
	view.AdminJobsPage(rows, allTags, user).Render(ctx, w)
}

// ── Job action handlers ────────────────────────────────────────

func (h *Handler) setJobDisabled(w http.ResponseWriter, r *http.Request) {
	path := "/jobs/" + r.PathValue("path")
	disabled := boolParam(r, "disabled")
	if err := h.repo.SetToolDisabled(r.Context(), path, disabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/jobs", http.StatusFound)
}

func (h *Handler) setJobTags(w http.ResponseWriter, r *http.Request) {
	path := "/jobs/" + r.PathValue("path")
	r.ParseForm()
	ids := dedupNonEmpty(r.Form["tag_ids[]"])
	if err := h.repo.SetToolTags(r.Context(), path, ids); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/jobs", http.StatusFound)
}

// ── Tool action handlers ───────────────────────────────────────

func (h *Handler) setToolVisibility(w http.ResponseWriter, r *http.Request) {
	path := "/tools/" + r.PathValue("path")
	vis := entity.ToolVisibility(r.FormValue("visibility"))
	if vis != entity.VisibilityPublic && vis != entity.VisibilityPrivate {
		http.Error(w, "invalid visibility", http.StatusBadRequest)
		return
	}
	if err := h.repo.SetToolVisibility(r.Context(), path, vis); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/tools", http.StatusFound)
}

func (h *Handler) setToolDisabled(w http.ResponseWriter, r *http.Request) {
	path := "/tools/" + r.PathValue("path")
	disabled := boolParam(r, "disabled")
	if err := h.repo.SetToolDisabled(r.Context(), path, disabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/tools", http.StatusFound)
}

func (h *Handler) setToolTags(w http.ResponseWriter, r *http.Request) {
	path := "/tools/" + r.PathValue("path")
	r.ParseForm()
	ids := dedupNonEmpty(r.Form["tag_ids[]"])
	if err := h.repo.SetToolTags(r.Context(), path, ids); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/tools", http.StatusFound)
}

// ── Tag CRUD handlers ──────────────────────────────────────────

func (h *Handler) listTagsJSON(w http.ResponseWriter, r *http.Request) {
	tags, err := h.repo.ListTags(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, len(tags))
	for i, t := range tags {
		out[i] = tagToMap(t)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handler) createTag(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	isGroup := boolParam(r, "is_group")
	isFilter := boolParam(r, "is_filter")
	tag, err := h.repo.CreateTag(r.Context(), name, isGroup, isFilter)
	if err != nil {
		if errors.Is(err, ErrTagNameTaken) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tagToMap(tag))
		return
	}
	http.Redirect(w, r, "/admin/tags", http.StatusFound)
}

func (h *Handler) updateTag(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := strings.TrimSpace(r.FormValue("name"))
	desc := r.FormValue("description")
	isGroup := boolParam(r, "is_group")
	isFilter := boolParam(r, "is_filter")
	sortOrder, _ := strconv.Atoi(r.FormValue("sort_order"))
	if err := h.repo.UpdateTag(r.Context(), id, name, desc, isGroup, isFilter, sortOrder); err != nil {
		if errors.Is(err, ErrTagNameTaken) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/tags", http.StatusFound)
}

func (h *Handler) deleteTag(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.repo.DeleteTag(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/tags", http.StatusFound)
}

// ── helpers ───────────────────────────────────────────────────

func dedupNonEmpty(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func boolParam(r *http.Request, key string) bool {
	v := r.FormValue(key)
	return v == "on" || v == "true" || v == "1"
}

func tagToMap(t *entity.Tag) map[string]any {
	return map[string]any{
		"id":          t.ID,
		"name":        t.Name,
		"description": t.Description,
		"is_group":    t.IsGroup,
		"is_filter":   t.IsFilter,
		"sort_order":  t.SortOrder,
	}
}
