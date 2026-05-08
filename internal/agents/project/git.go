package project

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// runGit runs `git <args>` with cwd. Empty cwd uses the current dir.
// Combined stdout/stderr are folded into the error so callers see why
// git failed (auth issue, missing binary, etc.).
func runGit(ctx context.Context, cwd string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, string(out))
	}
	return nil
}

// gitEnv returns the env passed to every git invocation. We force a
// stable identity so `git commit --allow-empty` (used during init of
// repo-less projects) doesn't fail on hosts where user.name /
// user.email are unset, and disable terminal prompts so a clone with
// bad creds errors fast instead of hanging.
func gitEnv() []string {
	env := os.Environ()
	env = append(env,
		"GIT_AUTHOR_NAME=wick",
		"GIT_AUTHOR_EMAIL=wick@localhost",
		"GIT_COMMITTER_NAME=wick",
		"GIT_COMMITTER_EMAIL=wick@localhost",
		"GIT_TERMINAL_PROMPT=0",
	)
	return env
}

// MaterializeWorkspace creates the project workspace either by cloning
// the remote or by `git init` + initial empty commit. Either way we
// end up with a usable git repo so later `git worktree add` calls from
// the session layer succeed. Exported because the session package
// (worktree management) shares the same git plumbing.
func MaterializeWorkspace(ctx context.Context, workspace, repoURL string) error {
	if repoURL != "" {
		return runGit(ctx, "", "clone", repoURL, workspace)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return err
	}
	if err := runGit(ctx, workspace, "init"); err != nil {
		return err
	}
	return runGit(ctx, workspace, "commit", "--allow-empty", "-m", "wick: initial empty commit")
}

// AddWorktree shells out to `git worktree add` from master, creating
// branch `branch` rooted at worktreePath.
func AddWorktree(ctx context.Context, master, worktreePath, branch string) error {
	return runGit(ctx, master, "worktree", "add", "-b", branch, worktreePath)
}

// RemoveWorktree shells out to `git worktree remove --force`. Force is
// OK because the design treats session worktrees as disposable.
func RemoveWorktree(ctx context.Context, master, worktreePath string) error {
	return runGit(ctx, master, "worktree", "remove", "--force", worktreePath)
}
