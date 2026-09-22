package agents

import (
	"net/http"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// assigneeOption is one person a ticket can be put on: an id to store and a
// name to draw. Deliberately nothing else — a ticket picker is not a user
// directory, and an email address here would make it one.
type assigneeOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// apiProjectAssignees handles GET /api/projects/{id}/assignees — who this
// project's tickets may be assigned to.
//
// The field used to be a text box that took a wick user id, which meant
// assigning a ticket required knowing somebody's uuid. This is what turns it
// into a picker of names.
//
// Scoped to callers who can already reach the project: the board they are
// looking at prints assignee names anyway, so the list adds no one they
// could not already see named. Only APPROVED users are offered — an
// unapproved account cannot act on a ticket, and offering it would produce
// work assigned to somebody who will never see it.
func apiProjectAssignees(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	if _, ok := globalMgr.Registry().Project(id); !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if !callerProjectAccess(c).allowProject(id) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	out := []assigneeOption{}
	if globalDB == nil {
		c.JSON(http.StatusOK, map[string]any{"assignees": out})
		return
	}
	var users []entity.User
	if err := globalDB.Select("id", "name", "email", "approved").
		Where("approved = ?", true).Find(&users).Error; err != nil {
		log.Warn().Err(err).Msg("agents: loading ticket assignees failed")
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not load users"})
		return
	}
	for _, u := range users {
		name := strings.TrimSpace(u.Name)
		if name == "" {
			// An account created from a channel may carry no name yet. The
			// local part of the email still reads as a person, which beats
			// showing a uuid in the dropdown.
			if at := strings.IndexByte(u.Email, '@'); at > 0 {
				name = u.Email[:at]
			}
		}
		if name == "" {
			name = u.ID
		}
		out = append(out, assigneeOption{ID: u.ID, Name: name})
	}
	// By name: the list is read, and ids sort by nothing a person can see.
	sort.Slice(out, func(i, j int) bool {
		if strings.EqualFold(out[i].Name, out[j].Name) {
			return out[i].ID < out[j].ID
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	c.JSON(http.StatusOK, map[string]any{"assignees": out})
}
