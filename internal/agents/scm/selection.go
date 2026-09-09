package scm

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Selection is the repo a session is working in: which one, where it
// sits on disk, and how that answer was reached.
type Selection struct {
	// Rel is the repo handle relative to the session cwd ("." when the
	// cwd is itself a repo). Empty when the cwd holds no repo at all.
	Rel string `json:"rel"`
	// Name is the basename, for display.
	Name string `json:"name"`
	// Dir is the absolute path — what an agent actually edits in.
	Dir string `json:"dir"`
	// Explicit is true when a human (or the agent) picked this repo,
	// false when it is just the first one discovered. The difference
	// matters in the system prompt: a default is a guess worth stating
	// as one, an explicit pick is an instruction.
	Explicit bool `json:"explicit"`
	// Total is how many repos the session cwd holds.
	Total int `json:"total"`
}

// ResolveSelection turns a stored repo handle into the selection for a
// session cwd. A stored handle that no longer exists (repo deleted,
// session moved) falls back to the first repo rather than erroring —
// a stale pointer must not break the panel or the prompt.
func ResolveSelection(cwd, stored string) (Selection, error) {
	repos, err := DiscoverRepos(cwd)
	if err != nil {
		return Selection{}, err
	}
	sel := Selection{Total: len(repos)}
	if len(repos) == 0 {
		return sel, nil
	}
	pick := repos[0]
	stored = strings.TrimSpace(stored)
	if stored != "" {
		for _, r := range repos {
			if r.Rel == stored {
				pick, sel.Explicit = r, true
				break
			}
		}
	}
	dir, err := ResolveRepoDir(cwd, pick.Rel)
	if err != nil {
		return sel, err
	}
	sel.Rel, sel.Name, sel.Dir = pick.Rel, pick.Name, dir
	return sel, nil
}

// ValidateRepo reports the canonical handle for rel under cwd, erroring
// when it names no repo. Used before storing a selection so a typo is
// refused at the point it is made rather than silently ignored later.
func ValidateRepo(cwd, rel string) (Repo, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return Repo{}, fmt.Errorf("repo is required")
	}
	repos, err := DiscoverRepos(cwd)
	if err != nil {
		return Repo{}, err
	}
	for _, r := range repos {
		if r.Rel == rel {
			return r, nil
		}
	}
	// Accept an absolute path or a basename too — an agent is more
	// likely to say "wick" or the path it has been editing than the
	// exact relative handle.
	for _, r := range repos {
		dir, derr := ResolveRepoDir(cwd, r.Rel)
		if derr == nil && (dir == filepath.Clean(rel) || r.Name == rel) {
			return r, nil
		}
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Rel)
	}
	sort.Strings(names)
	if len(names) > 20 {
		names = append(names[:20], "…")
	}
	return Repo{}, fmt.Errorf("no repo %q under the session cwd (have: %s)", rel, strings.Join(names, ", "))
}

// RemoteURL returns the URL configured for a remote, with any embedded
// credentials left as git reports them (callers strip before display).
// Empty string and no error when the remote is not configured — a repo
// with no remote is a normal local repo, not a failure.
func RemoteURL(ctx context.Context, dir, remote string) (string, error) {
	if remote == "" {
		remote = "origin"
	}
	out, err := run(ctx, dir, "remote", "get-url", remote)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

// RemoteHost extracts the host from a git remote URL, covering both the
// https://host/owner/repo and the git@host:owner/repo forms. Empty when
// the URL is a local path or unparseable.
func RemoteHost(rawURL string) string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return ""
	}
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if slash := strings.IndexAny(rest, "/:"); slash >= 0 {
			rest = rest[:slash]
		}
		return rest
	}
	// scp-like: [user@]host:path
	if at := strings.Index(u, "@"); at >= 0 {
		rest := u[at+1:]
		if colon := strings.Index(rest, ":"); colon > 0 {
			return rest[:colon]
		}
	}
	return ""
}
