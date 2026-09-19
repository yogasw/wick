package agents

import (
	"net/http"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/pkg/tool"
)

// api_todos.go serves the session's checklists to the UI.
//
// The todo tool's output already appears in the trace, but a trace is a
// record of what was said, not a view of what is true now: the live list
// scrolls away, and after a reload nobody can tell which of the five
// checklists on screen is the current one. This endpoint answers that in one
// call — the active list, and the ones that came before it.

// TodoItemDTO is one checklist item as the UI needs it. `label` is resolved
// server-side so the client does not re-implement the title/step fallback.
type TodoItemDTO struct {
	ID          string           `json:"id,omitempty"`
	Label       string           `json:"label"`
	Description string           `json:"description,omitempty"`
	Status      string           `json:"status"`
	Substeps    []TodoSubstepDTO `json:"substeps,omitempty"`
	Progress    *TodoProgressDTO `json:"progress,omitempty"`
	Detail      *TodoDetailDTO   `json:"detail,omitempty"`
}

// TodoProgressDTO is how far into one step the work has got — the bar under
// a step that is long rather than merely unfinished.
type TodoProgressDTO struct {
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Label string `json:"label,omitempty"`
}

// TodoDetailDTO is the payload an item carries: a log tail, a JSON result,
// a small table. Passed through as stored — the client decides how to draw
// each format, and nothing here interprets it.
type TodoDetailDTO struct {
	Format string `json:"format,omitempty"`
	Body   string `json:"body"`
}

// TodoSubstepDTO is one nested step.
type TodoSubstepDTO struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

// TodoListDTO is one checklist, with the counts the UI would otherwise
// compute in three places.
type TodoListDTO struct {
	Items     []TodoItemDTO `json:"items"`
	Total     int           `json:"total"`
	Completed int           `json:"completed"`
	Done      bool          `json:"done"`
	// Stopped says the work was abandoned rather than finished — the two
	// look identical in a half-ticked list, and only one of them means
	// anything got done.
	Stopped   bool   `json:"stopped,omitempty"`
	Note      string `json:"note,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// TodosDTO is the whole record for one session.
type TodosDTO struct {
	Active  *TodoListDTO  `json:"active,omitempty"`
	History []TodoListDTO `json:"history"`
}

func todoListDTO(l session.TodoList) TodoListDTO {
	out := TodoListDTO{
		Items:   make([]TodoItemDTO, 0, len(l.Items)),
		Total:   len(l.Items),
		Done:    l.Done,
		Stopped: l.Stopped,
		Note:    l.Note,
	}
	for _, it := range l.Items {
		if it.Status == "completed" {
			out.Completed++
		}
		subs := make([]TodoSubstepDTO, 0, len(it.Substeps))
		for _, s := range it.Substeps {
			subs = append(subs, TodoSubstepDTO{Step: s.Step, Status: s.Status})
		}
		row := TodoItemDTO{
			ID:          it.ID,
			Label:       it.Label(),
			Description: it.Description,
			Status:      it.Status,
			Substeps:    subs,
		}
		if it.Progress != nil {
			row.Progress = &TodoProgressDTO{Done: it.Progress.Done, Total: it.Progress.Total, Label: it.Progress.Label}
		}
		if it.Detail != nil {
			row.Detail = &TodoDetailDTO{Format: it.Detail.Format, Body: it.Detail.Body}
		}
		out.Items = append(out.Items, row)
	}
	if !l.StartedAt.IsZero() {
		out.StartedAt = l.StartedAt.Format(time.RFC3339)
	}
	if !l.UpdatedAt.IsZero() {
		out.UpdatedAt = l.UpdatedAt.Format(time.RFC3339)
	}
	return out
}

// apiSessionTodos handles GET /api/sessions/{id}/todos.
func apiSessionTodos(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if !callerProjectAccess(c).allowSession(sess.Meta.ProjectID, sess.Meta.UserID, sess.Meta.Participants) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	rec, err := session.LoadTodos(globalLayout, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// History newest-first: the panel reads downward, and the list somebody
	// wants to look back at is nearly always the one just finished.
	dto := TodosDTO{History: make([]TodoListDTO, 0, len(rec.History))}
	for i := len(rec.History) - 1; i >= 0; i-- {
		dto.History = append(dto.History, todoListDTO(rec.History[i]))
	}
	if rec.Active != nil {
		a := todoListDTO(*rec.Active)
		dto.Active = &a
	}
	c.JSON(http.StatusOK, dto)
}
