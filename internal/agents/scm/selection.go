package scm

import (
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
