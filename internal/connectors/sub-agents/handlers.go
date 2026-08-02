package subagents

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// delegateMaxTaskRunes caps a delegated task so a runaway prompt cannot
// bloat the row or the child's first turn.
const delegateMaxTaskRunes = 8000

type handlers struct{ deps Deps }

func newHandlers(deps Deps) *handlers { return &handlers{deps: deps} }

/* ── delegation ──────────────────────────────────────────────────────── */

type roleItem struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Provider    string `json:"provider"`
	MaxTurns    int    `json:"default_max_turns"`
	// Scope is "project" for a role this project owns and "global"
	// otherwise. Surfaced so the agent can tell a project's own role from
	// one every project inherits.
	Scope string `json:"scope"`
}

func (h *handlers) listAgents(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), false)
	if err != nil {
		return nil, err
	}
	profiles, err := h.deps.visibleRoles(c.Context(), caller)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	out := make([]roleItem, 0, len(profiles))
	for _, p := range profiles {
		scope := "global"
		if p.ProjectID != "" {
			scope = "project"
		}
		out = append(out, roleItem{
			Key: p.Key, Name: p.Name, Description: p.Description,
			Provider: p.Provider, MaxTurns: p.DefaultMaxTurns, Scope: scope,
		})
	}
	return map[string]any{"agents": out}, nil
}

func (h *handlers) delegate(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}

	profileKey := strings.TrimSpace(c.Input("profile"))
	if profileKey == "" {
		return nil, errors.New("profile is required — call list_agents to see the available keys")
	}
	task := strings.TrimSpace(c.Input("task"))
	if task == "" {
		return nil, errors.New("task is required")
	}
	if runes := []rune(task); len(runes) > delegateMaxTaskRunes {
		return nil, fmt.Errorf("task too long (max %d characters)", delegateMaxTaskRunes)
	}

	// Resolved in the caller's project scope, so a role this project
	// defines shadows the global one of the same key.
	profile, err := h.deps.svc().Repo.GetProfileScoped(c.Context(), caller.projectID, profileKey)
	if err != nil {
		if errors.Is(err, delegation.ErrProfileNotFound) {
			return nil, fmt.Errorf("no such role: %s — call list_agents for the list", profileKey)
		}
		return nil, fmt.Errorf("resolve role: %w", err)
	}
	// Same wording as "not found": do not confirm the existence of a role
	// this caller may not use.
	if len(delegation.VisibleProfiles([]entity.AgentProfile{*profile}, caller.tagIDs, caller.user.IsAdmin())) == 0 {
		return nil, fmt.Errorf("no such role: %s — call list_agents for the list", profileKey)
	}

	mode := strings.TrimSpace(c.Input("mode"))
	if !delegation.ValidMode(mode) {
		return nil, errors.New("mode must be 'sync' or 'async'")
	}
	sink := strings.TrimSpace(c.Input("delivery_sink"))
	if !delegation.ValidSink(sink) {
		return nil, errors.New("delivery_sink must be 'channel', 'session', or 'none'")
	}
	workspace := strings.TrimSpace(c.Input("workspace"))
	if !delegation.ValidWorkspace(workspace) {
		return nil, errors.New("workspace must be 'shared' or 'worktree'")
	}

	req := delegation.Request{
		ProfileKey:      profileKey,
		Task:            task,
		Context:         strings.TrimSpace(c.Input("context")),
		MaxTurns:        c.InputInt("max_turns"),
		MaxTokens:       c.InputInt("max_tokens"),
		Mode:            mode,
		DeliverySink:    sink,
		Workspace:       workspace,
		ParentSessionID: caller.sessionID,
		ProjectID:       caller.projectID,
		TriggeredBy:     caller.user.ID,
	}
	// A leader that is itself a sub-agent inherits its parent's root,
	// depth and ancestor chain — that inheritance is what makes the depth
	// limit and the cycle guard work at all.
	if parent, perr := h.deps.svc().Repo.FindByChildSession(c.Context(), caller.sessionID); perr == nil && parent != nil {
		req.RootID = parent.RootID
		req.Depth = parent.Depth + 1
		req.AncestorKeys = delegation.Ancestors(parent)
		if parent.TriggeredBy != "" {
			req.TriggeredBy = parent.TriggeredBy
		}
	}

	res, err := h.deps.svc().Run(c.Context(), req)
	if err != nil {
		// A governor refusal is actionable guidance rather than a crash:
		// its message tells the model what to do instead.
		var refusal *delegation.Refusal
		if errors.As(err, &refusal) {
			return nil, errors.New(refusal.Message)
		}
		return nil, fmt.Errorf("delegate: %w", err)
	}
	return res, nil
}

func (h *handlers) collect(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(c.Input("delegation_id"))
	if id == "" {
		items, cerr := h.deps.svc().CollectPending(c.Context(), caller.sessionID)
		if cerr != nil {
			return nil, fmt.Errorf("collect: %w", cerr)
		}
		return map[string]any{"results": items}, nil
	}

	res, err := h.deps.svc().Collect(c.Context(), id, caller.user.ID, caller.user.IsAdmin())
	if err != nil {
		switch {
		case errors.Is(err, delegation.ErrForbidden):
			return nil, errors.New("that delegation belongs to someone else")
		case errors.Is(err, delegation.ErrDelegationNotFound):
			return nil, fmt.Errorf("no such delegation: %s", id)
		default:
			return nil, fmt.Errorf("collect: %w", err)
		}
	}
	return res, nil
}

/* ── roles ───────────────────────────────────────────────────────────── */

// createAgent creates or updates a role in the CALLER'S PROJECT.
//
// Project scope is not a limitation bolted on — it is what makes this op
// safe to expose to every user. A project role is reachable only from
// sessions in that project, so the blast radius of an agent inventing a
// role is the project it is already working in. Global roles are
// reachable from everywhere and stay admin-only, created through the
// admin API instead.
func (h *handlers) createAgent(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}
	if caller.projectID == "" {
		return nil, errors.New(
			"this conversation is not in a project, so there is no scope to create a role in. " +
				"Move the session into a project first, or ask an admin to add a global role.")
	}

	key := strings.TrimSpace(c.Input("key"))
	if key == "" {
		return nil, errors.New("key is required")
	}
	description := strings.TrimSpace(c.Input("description"))
	if description == "" {
		return nil, errors.New(
			"description is required — it is what a delegating agent reads to decide when to pick this role")
	}
	systemPrompt := strings.TrimSpace(c.Input("system_prompt"))
	if systemPrompt == "" {
		return nil, errors.New(
			"system_prompt is required — without it the role has no instructions and behaves like a generic assistant")
	}

	provider := strings.TrimSpace(c.Input("provider"))
	if provider == "" {
		provider = "claude"
	}
	name := strings.TrimSpace(c.Input("name"))
	if name == "" {
		name = key
	}

	// Scope-exact, never resolved: a resolved lookup would find a GLOBAL
	// role of the same key and overwrite it, silently editing the role
	// every other project sees.
	existing, err := h.deps.svc().Repo.GetProfileExact(c.Context(), caller.projectID, key)
	if err != nil && !errors.Is(err, delegation.ErrProfileNotFound) {
		return nil, fmt.Errorf("look up role: %w", err)
	}

	p := &entity.AgentProfile{
		ProjectID:    caller.projectID,
		Key:          key,
		Name:         name,
		Description:  description,
		Provider:     provider,
		Model:        strings.TrimSpace(c.Input("model")),
		SystemPrompt: systemPrompt,
		// Inherit the caller's tags in full rather than narrowing: an
		// agent has no way to reason about which tags matter, and an
		// empty list is the safe default because effective access is
		// still intersected with the triggering human's own tags.
		AllowedTagIDs:      "[]",
		AllowedNativeTools: "[]",
		StrictMCP:          true,
		DefaultMaxTurns:    c.InputInt("max_turns"),
		DefaultMode:        delegation.ModeSync,
		DefaultWorkspace:   delegation.WorkspaceShared,
		CreatedBy:          caller.user.ID,
	}
	if p.DefaultMaxTurns <= 0 {
		p.DefaultMaxTurns = delegation.DefaultMaxTurns
	}
	created := true
	if existing != nil {
		created = false
		p.ID = existing.ID
		p.CreatedBy = existing.CreatedBy
		p.CreatedAt = existing.CreatedAt
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if err := h.deps.svc().Repo.SaveProfile(c.Context(), p); err != nil {
		return nil, fmt.Errorf("save role: %w", err)
	}

	status := "updated"
	if created {
		status = "created"
	}
	return map[string]any{
		"status": status, "key": p.Key, "scope": "project", "project_id": p.ProjectID,
		"note": "Visible only inside this project. Delegate to it with the `delegate` op.",
	}, nil
}

/* ── task boards ─────────────────────────────────────────────────────── */

type taskItem struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	Stage      string `json:"stage"`
	ClaimState string `json:"claim_state"`
	ProfileKey string `json:"profile_key,omitempty"`
	Priority   int    `json:"priority,omitempty"`
	Result     string `json:"result,omitempty"`
}

func tasksToItems(tasks []entity.AgentTask) []taskItem {
	out := make([]taskItem, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskItem{
			ID: t.ID, Title: t.Title, Body: t.Body, Stage: t.Stage,
			ClaimState: t.ClaimState, ProfileKey: t.ProfileKey,
			Priority: t.Priority, Result: t.Result,
		})
	}
	return out
}

// startErrText turns a claim-guard failure into guidance rather than a
// bare error, so the model corrects its sequence instead of retrying.
func startErrText(err error) error {
	if errors.Is(err, delegation.ErrTaskClaimed) {
		return errors.New("you do not hold that task — claim it first, and note another worker may have taken it")
	}
	return err
}

// tasks drives a shared board. Every action routes through the same repo
// methods the board UI uses, so the evidence gate and the claim guard
// apply identically to an agent and to a human — a gate enforced only on
// the web path would be bypassed by exactly the caller it constrains.
func (h *handlers) tasks(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}
	repo := h.deps.svc().Repo

	boardKey := strings.TrimSpace(c.Input("board"))
	if boardKey == "" {
		return nil, errors.New("board is required")
	}
	board, err := repo.GetBoard(c.Context(), boardKey)
	if err != nil {
		if errors.Is(err, delegation.ErrBoardNotFound) {
			return nil, fmt.Errorf("no such board: %s", boardKey)
		}
		return nil, fmt.Errorf("resolve board: %w", err)
	}
	if board.Disabled {
		return nil, fmt.Errorf("board %s is disabled", boardKey)
	}

	// The worker identity is the calling session: a claim must be held by
	// something that can be found again and released if it dies.
	workerID := caller.sessionID

	action := strings.TrimSpace(c.Input("action"))
	switch action {
	case "list":
		tasks, lerr := repo.ListTasks(c.Context(), board.ID)
		if lerr != nil {
			return nil, fmt.Errorf("list tasks: %w", lerr)
		}
		return map[string]any{"tasks": tasksToItems(tasks)}, nil

	case "add":
		title := strings.TrimSpace(c.Input("title"))
		if title == "" {
			return nil, errors.New("title is required to add a task")
		}
		t := &entity.AgentTask{
			BoardID:    board.ID,
			Title:      title,
			Body:       strings.TrimSpace(c.Input("body")),
			ProfileKey: strings.TrimSpace(c.Input("profile")),
			Priority:   c.InputInt("priority"),
			// New work lands in `ready`, not `backlog`: a task an agent
			// enqueued is meant to be picked up, and parking it in backlog
			// would leave it invisible to every worker.
			Stage:     entity.StageReady,
			ColumnID:  "ready",
			CreatedBy: caller.user.ID,
		}
		if cerr := repo.CreateTask(c.Context(), t); cerr != nil {
			return nil, fmt.Errorf("add task: %w", cerr)
		}
		return map[string]any{"task_id": t.ID, "status": "added"}, nil

	case "claim":
		t, cerr := repo.ClaimTask(c.Context(), board.ID, workerID, strings.TrimSpace(c.Input("profile")))
		if cerr != nil {
			return nil, fmt.Errorf("claim: %w", cerr)
		}
		if t == nil {
			return map[string]any{
				"claimed": false,
				"note":    "No ready tasks on this board. Do not poll in a loop — do other work and check again later.",
			}, nil
		}
		return map[string]any{
			"claimed": true, "task_id": t.ID, "title": t.Title, "body": t.Body,
			"note": "You hold this task. Call action='start' when you begin, then 'complete' or 'fail' when done.",
		}, nil

	case "start":
		id := strings.TrimSpace(c.Input("task_id"))
		if id == "" {
			return nil, errors.New("task_id is required")
		}
		if serr := repo.StartTask(c.Context(), id, workerID, ""); serr != nil {
			return nil, startErrText(serr)
		}
		return map[string]any{"status": "started", "task_id": id}, nil

	case "complete", "fail":
		id := strings.TrimSpace(c.Input("task_id"))
		if id == "" {
			return nil, errors.New("task_id is required")
		}
		failed := action == "fail"
		evidence := strings.TrimSpace(c.Input("evidence"))
		result := strings.TrimSpace(c.Input("result"))

		if cerr := repo.CompleteTask(c.Context(), board, id, workerID, result, evidence, failed); cerr != nil {
			if errors.Is(cerr, delegation.ErrEvidenceRequired) {
				return nil, errors.New(
					"this board requires evidence before a task can be completed — supply `evidence`: " +
						"test output, a report, or a link that shows the work actually landed")
			}
			return nil, startErrText(cerr)
		}
		out := map[string]any{"status": "completed", "task_id": id}
		if failed {
			out["status"] = "failed"
		}
		if warn := delegation.GateWarning(board, evidence); warn != "" {
			out["warning"] = warn
		}
		return out, nil

	default:
		return nil, errors.New("action must be one of: list, add, claim, start, complete, fail")
	}
}
