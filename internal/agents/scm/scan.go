// Package scm provides git source-control operations over a session's
// working directory. It shells out to the real `git` CLI (via
// pkg/safeexec) so existing SSH/PAT auth, branch behavior, and
// push/pull work exactly as on the command line.
//
// A session cwd can hold MANY cloned repos; DiscoverRepos walks the tree
// and returns every git repo root found under it.
package scm

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
)

// linkRoot is the only tree a symlinked repo may live in (wick's projects
// dir). Empty means symlinks are never followed.
var linkRoot atomic.Value // string

// SetLinkRoot allows a symlink inside a session cwd to stand in for a repo
// that lives under dir. Pass "" to turn it off.
func SetLinkRoot(dir string) {
	if dir != "" {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			dir = r
		}
	}
	linkRoot.Store(dir)
}

// LinkedRepoTarget reports where symlink p really points when it is a link
// to a git repo ROOT inside the link root. Anything else (a plain folder, a
// repo elsewhere on the host, a broken link) is not linked: the session
// boundary stays as strict as before for those.
func LinkedRepoTarget(p string) (string, bool) {
	root, _ := linkRoot.Load().(string)
	if root == "" {
		return "", false
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", false
	}
	if !strings.HasPrefix(real, root+string(filepath.Separator)) || !isRepoRoot(real) {
		return "", false
	}
	return real, true
}

// maxScanDepth bounds how deep DiscoverRepos descends below the root.
// Cloned repos normally sit one or two levels under the session cwd;
// 6 keeps the walk cheap while still catching nested layouts.
const maxScanDepth = 6

// maxNestedDepth bounds how far below a repo root we keep hunting for
// ANOTHER repo. A clone that lands inside a checkout sits at its top
// level or just under it, so 2 finds them without walking every source
// tree on the host.
const maxNestedDepth = 2

// skipDirs are directory names never worth descending into when hunting
// for .git roots. They never contain a sibling repo we care about and
// dominate the walk cost on real projects.
var skipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".cache":       true,
	"target":       true,
	".venv":        true,
	"__pycache__":  true,
	".codegraph":   true,
}

// Repo is a discovered git repository under a session cwd.
type Repo struct {
	// Rel is the repo root relative to the scan root, using forward
	// slashes. "." when the scan root is itself a repo. This is the
	// handle the HTTP layer accepts from clients.
	Rel string `json:"rel"`
	// Name is the basename for display ("." → the root's basename).
	Name string `json:"name"`
}

// DiscoverRepos walks root and returns every git repo root found,
// sorted by Rel. A directory containing a `.git` entry (dir OR file —
// worktrees/submodules use a `.git` file) is a repo root.
//
// The walk keeps going INSIDE a repo, up to maxNestedDepth below it, so
// a clone that landed inside another checkout is listed too. It has to
// be: the enclosing repo reports such a folder as one opaque change and
// cannot show what is inside it, so unless the nested repo appears here
// there is nowhere in the UI to see its files at all. A nested repo is
// named by its path ("erha-store/integration-helm") to keep it apart
// from a same-named repo at the top level.
//
// Returns an empty slice when root has no repos.
func DiscoverRepos(root string) ([]Repo, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rootDepth := strings.Count(filepath.ToSlash(abs), "/")

	var repos []Repo
	// Repo roots enclosing the dir being visited, outermost first. Walk
	// order is lexical DFS, so a parent is always pushed before its
	// children are seen.
	var enclosing []string
	walkErr := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable dir — skip it, keep walking the rest.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// A link to a repo is listed but never descended into, so a
			// link loop cannot make the walk endless.
			if _, ok := LinkedRepoTarget(p); ok && strings.Count(filepath.ToSlash(p), "/")-rootDepth <= maxScanDepth {
				for len(enclosing) > 0 && !strings.HasPrefix(p, enclosing[len(enclosing)-1]+string(filepath.Separator)) {
					enclosing = enclosing[:len(enclosing)-1]
				}
				rel := relSlash(abs, p)
				name := filepath.Base(p)
				if len(enclosing) > 0 {
					name = rel
				}
				repos = append(repos, Repo{Rel: rel, Name: name})
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		// Depth guard relative to root.
		if strings.Count(filepath.ToSlash(p), "/")-rootDepth > maxScanDepth {
			return fs.SkipDir
		}
		base := d.Name()
		// Never descend into noise dirs (but always allow the root itself).
		if p != abs && skipDirs[base] {
			return fs.SkipDir
		}
		// A repo's own internals are not a place to find repos, and they
		// used to be unreachable only because the walk stopped at the
		// repo root.
		if base == ".git" {
			return fs.SkipDir
		}
		// Drop enclosing repos we have walked out of, then bound how far
		// below the innermost one we keep looking.
		for len(enclosing) > 0 && !strings.HasPrefix(p, enclosing[len(enclosing)-1]+string(filepath.Separator)) {
			enclosing = enclosing[:len(enclosing)-1]
		}
		if len(enclosing) > 0 {
			inner := enclosing[len(enclosing)-1]
			if strings.Count(filepath.ToSlash(p), "/")-strings.Count(filepath.ToSlash(inner), "/") > maxNestedDepth {
				return fs.SkipDir
			}
		}
		// Is this dir a repo root?
		if isRepoRoot(p) {
			rel := relSlash(abs, p)
			name := repoName(abs, p)
			if len(enclosing) > 0 {
				name = rel // nested: the path is the only unambiguous label
			}
			repos = append(repos, Repo{Rel: rel, Name: name})
			enclosing = append(enclosing, p)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Rel < repos[j].Rel })
	if repos == nil {
		repos = []Repo{}
	}
	return repos, nil
}

// isRepoRoot reports whether dir contains a `.git` entry (dir or file).
func isRepoRoot(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// relSlash returns p relative to base in forward-slash form, "." when equal.
func relSlash(base, p string) string {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

// repoName returns the display basename for a repo at p under base.
func repoName(base, p string) string {
	if p == base {
		return filepath.Base(base)
	}
	return filepath.Base(p)
}

// ResolveRepoDir validates a client-supplied repo handle (Rel, relative
// to root) against the set actually discovered under root and returns
// its absolute path. This is the trust boundary: the HTTP layer must
// never pass a raw path to git — only a Rel that DiscoverRepos produced.
func ResolveRepoDir(root, rel string) (string, error) {
	repos, err := DiscoverRepos(root)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" {
		rel = "."
	}
	for _, r := range repos {
		if r.Rel == rel {
			abs, _ := filepath.Abs(root)
			if rel == "." {
				return abs, nil
			}
			return filepath.Join(abs, filepath.FromSlash(rel)), nil
		}
	}
	return "", ErrRepoNotFound
}
