package aimemory

// ai-memory's side of the Wiki and Handoffs tabs, plus the retention sweep.
//
// Three of the four are CLI calls. The fourth — cancelling ONE handoff — has
// no CLI form at all: `handoffs` offers only `--expire-all`, which discards
// every open baton including the ones other agents are waiting on. So this is
// the second (and last) MCP call in the feature, taken because the MCP path is
// SAFER than the CLI alternative rather than more convenient (PLAN §20.2). It
// reuses callTool from mcp.go; there is no second client.
//
// Every shape below was read off a real ai-memory 2.4.0 on 2026-09-25.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/agents/agentmemory"
)

// ReadPage runs `read-page --path <p> --json`.
//
// --path is passed rather than the positional query form: the positional one
// runs an FTS search and returns the TOP HIT, so a path that no longer exists
// would answer with some other page and look like a successful read.
func (s source) ReadPage(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope, path string) (*agentmemory.Page, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("%s read-page needs a path", binName)
	}
	out, err := s.run(ctx, sc.Dir, env(conn), args("read-page", conn, sc, "--path", path), nil)
	if err != nil {
		return nil, err
	}
	var raw pageJSON
	if err := decode(out, &raw); err != nil {
		return nil, fmt.Errorf("parse %s read-page: %w", binName, err)
	}
	return &agentmemory.Page{
		Path:        raw.Path,
		Workspace:   raw.Workspace,
		Project:     raw.Project,
		Title:       raw.Title,
		Body:        raw.Body,
		Frontmatter: raw.Frontmatter,
	}, nil
}

// Messages runs `message list --json`.
//
// The subcommand is two words, so it does not go through args(): that helper
// puts --data-dir straight after a single subcommand token, which would land
// between `message` and `list`.
func (s source) Messages(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope, box string, limit int) ([]agentmemory.Message, error) {
	cmd := []string{"message", "list"}
	if d := strings.TrimSpace(conn.DataDir); d != "" {
		cmd = append(cmd, "--data-dir", d)
	}
	if w := strings.TrimSpace(sc.Workspace); w != "" {
		cmd = append(cmd, "--workspace", w)
	}
	if p := strings.TrimSpace(sc.Project); p != "" {
		cmd = append(cmd, "--project", p)
	}
	if box == "outbox" {
		cmd = append(cmd, "--outbox")
	}
	if limit > 0 {
		cmd = append(cmd, "--limit", strconv.Itoa(limit))
	}
	out, err := s.run(ctx, sc.Dir, env(conn), append(cmd, "--json"), nil)
	if err != nil {
		return nil, err
	}
	var rows []messageJSON
	if err := decode(out, &rows); err != nil {
		return nil, fmt.Errorf("parse %s message list: %w", binName, err)
	}
	msgs := make([]agentmemory.Message, 0, len(rows))
	for _, r := range rows {
		msgs = append(msgs, agentmemory.Message{
			ID:              r.ID,
			Subject:         str(r.Subject),
			Body:            str(r.Body),
			FromAgent:       str(r.FromAgent),
			FromWorkspaceID: str(r.FromWorkspaceID),
			FromProjectID:   str(r.FromProjectID),
			State:           r.State,
			CreatedAt:       r.CreatedAt,
			ClaimedAt:       str(r.ClaimedAt),
		})
	}
	return msgs, nil
}

// CancelHandoff retires one handoff via `memory_handoff_cancel`.
//
// The scope is required for the same reason a briefing's is: a plain HTTP POST
// carries no cwd, so an unscoped call is answered about whichever project the
// daemon resolves from elsewhere — and here that would cancel a baton in a
// project nobody named.
//
// any_owner is deliberately never sent. The default only touches handoffs the
// caller owns or that were published to the whole project; opting out of that
// would let the panel consume a baton belonging to an operator who is simply
// away, which is a recovery action, not a tidy-up.
func (s source) CancelHandoff(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope, id string) (*agentmemory.HandoffCancelResult, error) {
	ws, proj := strings.TrimSpace(sc.Workspace), strings.TrimSpace(sc.Project)
	if ws == "" || proj == "" {
		return nil, fmt.Errorf("%s handoff cancel needs both a workspace and a project", binName)
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%s handoff cancel needs a handoff id", binName)
	}
	var out agentmemory.HandoffCancelResult
	if err := callTool(ctx, conn, "memory_handoff_cancel", map[string]any{
		"handoff_id": id,
		"workspace":  ws,
		"project":    proj,
	}, &out); err != nil {
		return nil, err
	}
	// cancelled:false comes back with isError:false — the handoff had
	// already expired or been accepted. That is a real answer, not a
	// failure, and it is returned as-is so the caller can say so.
	return &out, nil
}

// ForgetSweep runs `forget-sweep`. Like compact it has no --json mode, so its
// own line of prose is carried through rather than parsed into a shape the CLI
// never promised.
func (s source) ForgetSweep(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope, dryRun bool) (*agentmemory.SweepReport, error) {
	cmd := []string{"forget-sweep"}
	if d := strings.TrimSpace(conn.DataDir); d != "" {
		cmd = append(cmd, "--data-dir", d)
	}
	if w := strings.TrimSpace(sc.Workspace); w != "" {
		cmd = append(cmd, "--workspace", w)
	}
	if p := strings.TrimSpace(sc.Project); p != "" {
		cmd = append(cmd, "--project", p)
	}
	if dryRun {
		cmd = append(cmd, "--dry-run")
	}
	out, err := s.run(ctx, sc.Dir, env(conn), cmd, nil)
	if err != nil {
		return nil, err
	}
	return &agentmemory.SweepReport{DryRun: dryRun, Output: strings.TrimSpace(string(out))}, nil
}

// ── CLI JSON shapes ──────────────────────────────────────────────────

// pageJSON mirrors `read-page --json`. Frontmatter stays raw: its keys depend
// on the page kind, and dropping the ones wick did not model would hide the
// session_id and agent that make a session page traceable.
type pageJSON struct {
	Path        string          `json:"path"`
	Workspace   string          `json:"workspace"`
	Project     string          `json:"project"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Frontmatter json.RawMessage `json:"frontmatter"`
}

// messageJSON mirrors one row of `message list --json`.
//
// The sender is two UUIDs and nothing else. There is no name for them in this
// document, in the MCP equivalent, or in the project listing (which carries no
// ids to join on) — verified 2026-09-25. So they are carried as ids and the UI
// shows them as ids.
type messageJSON struct {
	ID              string  `json:"id"`
	Subject         *string `json:"subject"`
	Body            *string `json:"body"`
	FromAgent       *string `json:"from_agent"`
	FromWorkspaceID *string `json:"from_workspace_id"`
	FromProjectID   *string `json:"from_project_id"`
	State           string  `json:"state"`
	CreatedAt       string  `json:"created_at"`
	ClaimedAt       *string `json:"claimed_at"`
}
