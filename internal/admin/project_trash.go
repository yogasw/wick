package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	adminview "github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/agents/trash"
)

// ProjectTrash lists deleted projects and restores one. Wired by the
// server (SetProjectTrash) from the agents tool, which owns the registry a
// restore has to re-adopt into.
type ProjectTrash interface {
	DeletedProjects(ctx context.Context) ([]trash.Manifest, error)
	RestoreDeletedProject(ctx context.Context, key string) error
}

// SetProjectTrash wires the deleted-projects source. Optional: without it
// the Projects page shows active projects only.
func (h *Handler) SetProjectTrash(t ProjectTrash) { h.projectTrash = t }

// projectShow reads ?show= on /admin/projects: active (default), deleted
// or all.
func projectShow(r *http.Request) string {
	switch s := r.URL.Query().Get("show"); s {
	case adminview.ShowDeleted, adminview.ShowAll:
		return s
	}
	return adminview.ShowActive
}

// deletedProjectRows turns trash entries into Projects rows. Their owner
// and tag controls are not rendered (adminview.DeletedInfo), so they need
// no access summary.
func deletedProjectRows(ms []trash.Manifest, names map[string]string, now time.Time) []adminview.ResourceAdminRow {
	rows := make([]adminview.ResourceAdminRow, 0, len(ms))
	for _, m := range ms {
		left := int(m.DeletedAt.Add(trash.Retention).Sub(now).Hours() / 24)
		if left < 0 {
			left = 0
		}
		rows = append(rows, adminview.ResourceAdminRow{
			ID:         m.ID,
			Name:       m.Name,
			CreatedBy:  m.OwnerUserID,
			OwnerLabel: labelFor(names, m.OwnerUserID),
			Path:       "/projects/" + m.ID,
			Deleted: &adminview.DeletedInfo{
				Key:      m.Key,
				By:       deletedByLabel(names, m.DeletedBy),
				At:       m.DeletedAt,
				DaysLeft: left,
				Chats:    len(m.SessionIDs),
			},
		})
	}
	return rows
}

func deletedByLabel(names map[string]string, id string) string {
	if id == "" {
		return "system"
	}
	return labelFor(names, id)
}

// restoreDeletedProject handles POST /admin/projects-trash/{key}/restore.
func (h *Handler) restoreDeletedProject(w http.ResponseWriter, r *http.Request) {
	if h.projectTrash == nil {
		http.Error(w, "deleted projects not available", http.StatusServiceUnavailable)
		return
	}
	key := r.PathValue("key")
	if err := h.projectTrash.RestoreDeletedProject(r.Context(), key); err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, trash.ErrNotFound):
			status = http.StatusNotFound
		case strings.HasPrefix(err.Error(), "cannot restore"):
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	redirectOrNoContent(w, r, "/admin/projects?show=deleted")
}
