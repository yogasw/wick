package session

import (
	"context"
	"os"
	"path/filepath"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
)

// addWorktree creates the session's git worktree off the project's
// master clone, branched as `session/<id>`. Branch collisions surface
// as a git error (the design assumes session IDs are unique-by-
// construction).
func addWorktree(ctx context.Context, layout config.Layout, projectName, id string) error {
	master := layout.ProjectWorkspace(projectName)
	worktree := layout.SessionWorkspace(id)
	branch := worktreeBranch(id)
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		return err
	}
	return project.AddWorktree(ctx, master, worktree, branch)
}

// removeWorktree shells out to `git worktree remove --force`, then
// falls back to a filesystem delete if git refuses (so registry can't
// get wedged on a corrupt worktree state).
func removeWorktree(ctx context.Context, layout config.Layout, projectName, id string) error {
	master := layout.ProjectWorkspace(projectName)
	worktree := layout.SessionWorkspace(id)
	if err := project.RemoveWorktree(ctx, master, worktree); err != nil {
		_ = os.RemoveAll(worktree)
		return err
	}
	return nil
}

// worktreeBranch is the deterministic branch name used per session.
// Slack thread_ts contains a `.` which git allows in branch names, so
// no escaping needed.
func worktreeBranch(id string) string { return "session/" + id }
