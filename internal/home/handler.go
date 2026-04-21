package home

import (
	"context"
	"encoding/json"
	"github.com/yogasw/wick/internal/bookmark"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/tool"
	"net/http"
)

type Handler struct {
	allItems []tool.Tool
	authSvc  *login.Service
	tagsSvc  *tags.Service
	bookmark *bookmark.Service
}

func NewHandler(items []tool.Tool, authSvc *login.Service, tagsSvc *tags.Service, bookmarkSvc *bookmark.Service) *Handler {
	return &Handler{allItems: items, authSvc: authSvc, tagsSvc: tagsSvc, bookmark: bookmarkSvc}
}

const guestViewCookie = "_st_view"

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	if r.URL.Path != "/" {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	if r.Method == http.MethodPost {
		if v := r.FormValue("home_view"); v != "" {
			if user != nil {
				_ = h.authSvc.SetHomeView(r.Context(), user.ID, v)
				user.Metadata.HomeView = v
			} else {
				http.SetCookie(w, &http.Cookie{
					Name:     guestViewCookie,
					Value:    v,
					Path:     "/",
					MaxAge:   365 * 24 * 60 * 60,
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
				})
			}
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	visible := h.VisibleItems(r, user)
	groups, ungrouped := h.BuildGroups(r.Context(), user, visible)
	meta := h.ItemMeta(r.Context(), user, visible)
	viewMode := entity.HomeViewCompact
	if user != nil {
		viewMode = user.Metadata.HomeViewOrDefault()
	} else if c, err := r.Cookie(guestViewCookie); err == nil {
		viewMode = c.Value
	}
	IndexPage(visible, groups, ungrouped, meta, user, viewMode).Render(r.Context(), w)
}

// APITools returns the items the current user can access as JSON.
func (h *Handler) APITools(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	visible := h.VisibleItems(r, user)
	meta := h.ItemMeta(r.Context(), user, visible)
	type outTool struct {
		tool.Tool
		Tags       []string `json:"tags,omitempty"`
		Bookmarked bool     `json:"bookmarked,omitempty"`
	}
	out := make([]outTool, len(visible))
	for i, t := range visible {
		m := meta[t.Path]
		out[i] = outTool{Tool: t, Tags: m.GroupNames, Bookmarked: m.Bookmarked}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// ToolMetaEntry holds per-item display metadata.
type ToolMetaEntry struct {
	GroupNames []string
	Bookmarked bool
}

// ItemMeta returns a map from item path to display metadata.
func (h *Handler) ItemMeta(ctx context.Context, user *entity.User, visible []tool.Tool) map[string]ToolMetaEntry {
	out := make(map[string]ToolMetaEntry, len(visible))
	paths := make([]string, len(visible))
	for i, t := range visible {
		paths[i] = t.Path
	}
	toolTagIDs, _ := h.tagsSvc.ToolTagIDs(ctx, paths)
	groupTags, _ := h.tagsSvc.GroupTags(ctx)
	nameByID := make(map[string]string, len(groupTags))
	for _, g := range groupTags {
		nameByID[g.ID] = g.Name
	}
	var bookmarked map[string]bool
	if user != nil {
		bookmarked, _ = h.bookmark.ListForUser(ctx, user.ID)
	}
	for _, t := range visible {
		var names []string
		for _, id := range toolTagIDs[t.Path] {
			if n, ok := nameByID[id]; ok {
				names = append(names, n)
			}
		}
		out[t.Path] = ToolMetaEntry{GroupNames: names, Bookmarked: bookmarked[t.Path]}
	}
	return out
}

// VisibleItems returns the items the user can see on the home grid.
func (h *Handler) VisibleItems(r *http.Request, user *entity.User) []tool.Tool {
	var result []tool.Tool
	for _, t := range h.allItems {
		defaultVis := t.DefaultVisibility
		if defaultVis == "" {
			defaultVis = entity.VisibilityPrivate
		}
		if h.authSvc.CanAccessTool(r.Context(), user, t.Path, defaultVis) {
			result = append(result, t)
		}
	}
	return result
}

// Group represents one section on the home page.
type Group struct {
	Kind        string // "bookmarks" | "tag" | "category"
	Name        string
	Description string
	Tools       []tool.Tool
}

// BuildGroups arranges the visible items into display groups.
func (h *Handler) BuildGroups(ctx context.Context, user *entity.User, visible []tool.Tool) ([]Group, []tool.Tool) {
	groups := make([]Group, 0, 4)

	var bookmarked map[string]bool
	if user != nil {
		bookmarked, _ = h.bookmark.ListForUser(ctx, user.ID)
	}
	if len(bookmarked) > 0 {
		var bmTools []tool.Tool
		for _, t := range visible {
			if bookmarked[t.Path] {
				bmTools = append(bmTools, t)
			}
		}
		if len(bmTools) > 0 {
			groups = append(groups, Group{Kind: "bookmarks", Name: "Bookmarks", Tools: bmTools})
		}
	}

	groupTags, _ := h.tagsSvc.GroupTags(ctx)
	paths := make([]string, len(visible))
	for i, t := range visible {
		paths[i] = t.Path
	}
	toolTagIDs, _ := h.tagsSvc.ToolTagIDs(ctx, paths)
	pathToTagSet := make(map[string]map[string]bool, len(toolTagIDs))
	for p, ids := range toolTagIDs {
		set := make(map[string]bool, len(ids))
		for _, id := range ids {
			set[id] = true
		}
		pathToTagSet[p] = set
	}
	hasGroupTag := make(map[string]bool)
	for _, tag := range groupTags {
		var inGroup []tool.Tool
		for _, t := range visible {
			if pathToTagSet[t.Path][tag.ID] {
				inGroup = append(inGroup, t)
				hasGroupTag[t.Path] = true
			}
		}
		if len(inGroup) > 0 {
			groups = append(groups, Group{
				Kind:        "tag",
				Name:        tag.Name,
				Description: tag.Description,
				Tools:       inGroup,
			})
		}
	}

	var ungrouped []tool.Tool
	for _, t := range visible {
		if !hasGroupTag[t.Path] {
			ungrouped = append(ungrouped, t)
		}
	}
	return groups, ungrouped
}
