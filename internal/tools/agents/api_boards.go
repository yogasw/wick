package agents

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// Task board + Kanban HTTP surface (Phases 7 & 8).
//
// Every mutation routes through the same repo methods the MCP tools use,
// so the claim guard and the evidence gate behave identically whether a
// human drags a card or an agent calls wick_tasks. A rule enforced on
// only one of those paths is not a rule.

/* ── boards ─────────────────────────────────────────────────────────── */

// BoardItem is one board as the UI sees it.
type BoardItem struct {
	ID           string                   `json:"id"`
	Key          string                   `json:"key"`
	Name         string                   `json:"name"`
	Description  string                   `json:"description"`
	Columns      []delegation.BoardColumn `json:"columns"`
	SquadKey     string                   `json:"squad_key,omitempty"`
	GateMode     string                   `json:"gate_mode"`
	AutoDelegate bool                     `json:"auto_delegate"`
	Disabled     bool                     `json:"disabled"`
}

func boardToItem(b entity.AgentBoard) BoardItem {
	return BoardItem{
		ID: b.ID, Key: b.Key, Name: b.Name, Description: b.Description,
		Columns:  delegation.DecodeColumns(b.Columns),
		SquadKey: b.SquadKey, GateMode: b.GateMode,
		AutoDelegate: b.AutoDelegate, Disabled: b.Disabled,
	}
}

// apiBoardList handles GET /api/boards.
func apiBoardList(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) {
		return
	}
	u := login.GetUser(c.Context())
	boards, err := globalDelegation.Repo.ListBoards(c.Context(), u != nil && u.IsAdmin())
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	items := make([]BoardItem, 0, len(boards))
	for _, b := range boards {
		items = append(items, boardToItem(b))
	}
	c.JSON(http.StatusOK, map[string]any{"boards": items})
}

// apiBoardSave handles POST /api/boards — create or update. Admin-only:
// a board defines the gate policy every worker on it must satisfy.
func apiBoardSave(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) || !requireProfileAdmin(c) {
		return
	}
	var req BoardItem
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<20)).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "key is required"})
		return
	}
	if req.GateMode == "" {
		req.GateMode = entity.GateModeOff
	}
	switch req.GateMode {
	case entity.GateModeOff, entity.GateModeWarning, entity.GateModeBlocking:
	default:
		c.JSON(http.StatusBadRequest, map[string]string{"error": "gate_mode must be off, warning, or blocking"})
		return
	}
	if len(req.Columns) == 0 {
		req.Columns = delegation.DefaultColumns()
	}

	existing, err := globalDelegation.Repo.GetBoard(c.Context(), req.Key)
	if err != nil && !errors.Is(err, delegation.ErrBoardNotFound) {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	b := &entity.AgentBoard{
		ID: req.ID, Key: req.Key, Name: req.Name, Description: req.Description,
		Columns:  delegation.EncodeColumns(req.Columns),
		SquadKey: req.SquadKey, GateMode: req.GateMode,
		AutoDelegate: req.AutoDelegate, Disabled: req.Disabled,
	}
	if existing != nil {
		b.ID, b.CreatedBy, b.CreatedAt = existing.ID, existing.CreatedBy, existing.CreatedAt
	}
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	if b.CreatedBy == "" {
		if u := login.GetUser(c.Context()); u != nil {
			b.CreatedBy = u.ID
		}
	}
	if err := globalDelegation.Repo.SaveBoard(c.Context(), b); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, boardToItem(*b))
}

// apiBoardDelete handles DELETE /api/boards/{id}.
func apiBoardDelete(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) || !requireProfileAdmin(c) {
		return
	}
	if err := globalDelegation.Repo.DeleteBoard(c.Context(), c.PathValue("id")); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

/* ── tasks ──────────────────────────────────────────────────────────── */

// apiBoardTasks handles GET /api/boards/{key}/tasks — the Kanban view.
func apiBoardTasks(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) {
		return
	}
	board, ok := resolveBoard(c)
	if !ok {
		return
	}
	tasks, err := globalDelegation.Repo.ListTasks(c.Context(), board.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"board": boardToItem(*board),
		"tasks": tasks,
	})
}

// apiTaskCreate handles POST /api/boards/{key}/tasks.
func apiTaskCreate(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) {
		return
	}
	board, ok := resolveBoard(c)
	if !ok {
		return
	}
	var req struct {
		Title      string `json:"title"`
		Body       string `json:"body"`
		ProfileKey string `json:"profile_key"`
		Priority   int    `json:"priority"`
		Stage      string `json:"stage"`
	}
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<20)).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "title is required"})
		return
	}
	stage := req.Stage
	if stage == "" {
		stage = entity.StageBacklog
	}
	t := &entity.AgentTask{
		BoardID: board.ID, Title: req.Title, Body: req.Body,
		ProfileKey: req.ProfileKey, Priority: req.Priority, Stage: stage,
	}
	if u := login.GetUser(c.Context()); u != nil {
		t.CreatedBy = u.ID
	}
	if err := globalDelegation.Repo.CreateTask(c.Context(), t); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, t)
}

// apiTaskMove handles POST /api/tasks/{id}/move — the Kanban drag.
//
// Moving into a terminal column is a completion, so it runs the SAME
// evidence gate the tool path enforces; otherwise a drag would launder
// work past a rule agents have to obey.
func apiTaskMove(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) {
		return
	}
	var req struct {
		BoardKey string `json:"board_key"`
		ColumnID string `json:"column_id"`
		Position int    `json:"position"`
	}
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<20)).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	board, err := globalDelegation.Repo.GetBoard(c.Context(), req.BoardKey)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "board not found"})
		return
	}
	err = globalDelegation.Repo.MoveTask(c.Context(), board, c.PathValue("id"), req.ColumnID, req.Position)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, map[string]string{"status": "moved"})
	case errors.Is(err, delegation.ErrEvidenceRequired):
		c.JSON(http.StatusConflict, map[string]string{
			"error": "this board requires evidence before a task can be marked done",
		})
	case errors.Is(err, delegation.ErrTaskNotFound):
		c.JSON(http.StatusNotFound, map[string]string{"error": "task not found"})
	default:
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// apiTaskDelete handles DELETE /api/tasks/{id}.
func apiTaskDelete(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) {
		return
	}
	if err := globalDelegation.Repo.DeleteTask(c.Context(), c.PathValue("id")); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

func resolveBoard(c *tool.Ctx) (*entity.AgentBoard, bool) {
	board, err := globalDelegation.Repo.GetBoard(c.Context(), c.PathValue("key"))
	if err != nil {
		if errors.Is(err, delegation.ErrBoardNotFound) {
			c.JSON(http.StatusNotFound, map[string]string{"error": "board not found"})
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return nil, false
	}
	return board, true
}

/* ── squads ─────────────────────────────────────────────────────────── */

// SquadItem is one squad as the UI sees it.
type SquadItem struct {
	ID               string   `json:"id"`
	Key              string   `json:"key"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	LeaderProfileKey string   `json:"leader_profile_key"`
	MemberKeys       []string `json:"member_profile_keys"`
	Disabled         bool     `json:"disabled"`
}

// apiSquadList handles GET /api/squads.
func apiSquadList(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) {
		return
	}
	u := login.GetUser(c.Context())
	squads, err := globalDelegation.Repo.ListSquads(c.Context(), u != nil && u.IsAdmin())
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	items := make([]SquadItem, 0, len(squads))
	for _, sq := range squads {
		items = append(items, SquadItem{
			ID: sq.ID, Key: sq.Key, Name: sq.Name, Description: sq.Description,
			LeaderProfileKey: sq.LeaderProfileKey,
			MemberKeys:       delegation.SquadMembers(&sq),
			Disabled:         sq.Disabled,
		})
	}
	c.JSON(http.StatusOK, map[string]any{"squads": items})
}

// apiSquadSave handles POST /api/squads. Admin-only, like profiles: a
// squad shapes which roles run together.
func apiSquadSave(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) || !requireProfileAdmin(c) {
		return
	}
	var req SquadItem
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<20)).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "key is required"})
		return
	}
	existing, err := globalDelegation.Repo.GetSquad(c.Context(), req.Key)
	if err != nil && !errors.Is(err, delegation.ErrSquadNotFound) {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sq := &entity.AgentSquad{
		ID: req.ID, Key: req.Key, Name: req.Name, Description: req.Description,
		LeaderProfileKey:  req.LeaderProfileKey,
		MemberProfileKeys: delegation.EncodeSquadMembers(req.MemberKeys),
		Disabled:          req.Disabled,
	}
	if existing != nil {
		sq.ID, sq.CreatedBy, sq.CreatedAt = existing.ID, existing.CreatedBy, existing.CreatedAt
	}
	if sq.ID == "" {
		sq.ID = uuid.NewString()
	}
	if sq.CreatedBy == "" {
		if u := login.GetUser(c.Context()); u != nil {
			sq.CreatedBy = u.ID
		}
	}
	if err := globalDelegation.Repo.SaveSquad(c.Context(), sq); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, req)
}

// apiSquadDelete handles DELETE /api/squads/{id}.
func apiSquadDelete(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) || !requireProfileAdmin(c) {
		return
	}
	if err := globalDelegation.Repo.DeleteSquad(c.Context(), c.PathValue("id")); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}
