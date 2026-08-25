package delegation

import (
	"context"
	"fmt"
	"github.com/yogasw/wick/pkg/safeexec"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
)

// GitWorktrees gives each sub-agent its own git worktree, so parallel
// coding tasks cannot overwrite each other's edits (Phase 3).
//
// Policy for a project that is not a git repository: fall back to the
// shared workspace and SAY SO. Copying the directory instead would burn
// disk on large repos and needs its own cleanup rules; refusing outright
// would make coding roles unusable on non-git projects. Falling back is
// the useful default — as long as it is never silent, which is why every
// downgrade returns a note that reaches the leader.
type GitWorktrees struct {
	Layout agentconfig.Layout
}

// NewGitWorktrees builds the provider.
func NewGitWorktrees(layout agentconfig.Layout) *GitWorktrees {
	return &GitWorktrees{Layout: layout}
}

// PrepareWorktree creates a worktree for one sub-agent.
//
// Returns ("", note, nil) when isolation is not possible — the caller
// then runs in the shared workspace with that note attached. An error is
// reserved for a real failure, not for "this project cannot do it".
func (g *GitWorktrees) PrepareWorktree(ctx context.Context, projectID, childSessionID string) (string, string, error) {
	if projectID == "" {
		return "", "no project bound to this session; ran in the shared workspace", nil
	}
	if !project.Exists(g.Layout, projectID) {
		return "", "project not found; ran in the shared workspace", nil
	}
	base, err := project.ResolvePath(g.Layout, projectID)
	if err != nil {
		return "", "", err
	}
	if !isGitRepo(ctx, base) {
		return "", "project is not a git repository; ran in the shared workspace", nil
	}

	dir := filepath.Join(g.Layout.BaseDir, "worktrees", childSessionID)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", "", err
	}
	// Detached HEAD at the current commit: the sub-agent gets a private
	// checkout without creating a branch that would then need reaping,
	// and without moving the parent's HEAD.
	branch := "wick-sub/" + childSessionID
	cmd := safeexec.CommandContext(ctx, "git", "-C", base, "worktree", "add", "--detach", "-b", branch, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		// A worktree that cannot be created is not fatal to the task —
		// report it and let the caller fall back.
		return "", fmt.Sprintf("could not create a git worktree (%s); ran in the shared workspace", strings.TrimSpace(string(out))), nil
	}
	return dir, "", nil
}

// ReleaseWorktree removes a sub-agent's worktree.
//
// Best-effort: a delegation that already produced its answer must not be
// failed because cleanup did not work. Anything left behind is a stale
// directory, which is recoverable; losing the result is not.
func (g *GitWorktrees) ReleaseWorktree(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	cmd := safeexec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	if err := cmd.Run(); err != nil {
		// Not a live worktree any more; just drop the directory.
		return os.RemoveAll(path)
	}
	if out, err := safeexec.CommandContext(ctx, "git", "-C", path, "worktree", "remove", "--force", path).CombinedOutput(); err != nil {
		log.Debug().Str("path", path).Str("out", string(out)).Msg("delegation: worktree remove failed; removing directory")
		return os.RemoveAll(path)
	}
	return nil
}

// isGitRepo reports whether dir sits inside a git work tree.
func isGitRepo(ctx context.Context, dir string) bool {
	cmd := safeexec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}
