package scm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/safeexec"
)

// ErrRepoNotFound is returned when a client-supplied repo handle does
// not match any repo discovered under the session cwd.
var ErrRepoNotFound = errors.New("repo not found")

// Default timeouts. Network ops (push/pull) get a longer budget.
const (
	localTimeout = 20 * time.Second
	netTimeout   = 120 * time.Second
)

// GitError carries the failing command and git's own stderr so the HTTP
// layer can surface the real message (auth failure, merge conflict, …)
// instead of a bare exit code.
type GitError struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *GitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *GitError) Unwrap() error { return e.Err }

// run executes `git <args...>` in dir and returns stdout. stderr is
// captured into a GitError on failure.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := safeexec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), &GitError{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

// ── Status ──────────────────────────────────────────────────────────

// BranchInfo summarizes the current branch and its upstream tracking.
type BranchInfo struct {
	Name     string `json:"name"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Detached bool   `json:"detached"`
}

// FileChange is one entry in `git status`.
type FileChange struct {
	Path      string `json:"path"`
	OrigPath  string `json:"orig_path,omitempty"` // rename/copy source
	Index     string `json:"index"`               // staged status code (X)
	WorkTree  string `json:"work_tree"`           // unstaged status code (Y)
	Staged    bool   `json:"staged"`              // has staged changes
	Unstaged  bool   `json:"unstaged"`            // has worktree changes
	Untracked bool   `json:"untracked"`
}

// StatusResult bundles a repo's branch + change list.
type StatusResult struct {
	Branch  BranchInfo   `json:"branch"`
	Changes []FileChange `json:"changes"`
}

// Status runs `git status --porcelain=v2 --branch -z` and parses it.
func Status(ctx context.Context, dir string) (StatusResult, error) {
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	out, err := run(ctx, dir, "status", "--porcelain=v2", "--branch", "-z")
	if err != nil {
		return StatusResult{}, err
	}
	return parseStatus(out), nil
}

// parseStatus decodes porcelain v2 NUL-delimited output. Rename/copy
// entries (type 2) carry an extra NUL-separated origin path.
func parseStatus(out string) StatusResult {
	var res StatusResult
	res.Changes = []FileChange{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		line := fields[i]
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			name := strings.TrimPrefix(line, "# branch.head ")
			if name == "(detached)" {
				res.Branch.Detached = true
			} else {
				res.Branch.Name = name
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			res.Branch.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			// "+A -B"
			ab := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			for _, tok := range ab {
				n, _ := strconv.Atoi(strings.TrimLeft(tok, "+-"))
				if strings.HasPrefix(tok, "+") {
					res.Branch.Ahead = n
				} else if strings.HasPrefix(tok, "-") {
					res.Branch.Behind = n
				}
			}
		case strings.HasPrefix(line, "1 "):
			res.Changes = append(res.Changes, parseOrdinary(line))
		case strings.HasPrefix(line, "2 "):
			// Rename/copy: the path field after XY... is the new path,
			// and the ORIGINAL path is the next NUL-separated field.
			fc := parseOrdinary(line)
			if i+1 < len(fields) {
				fc.OrigPath = fields[i+1]
				i++
			}
			res.Changes = append(res.Changes, fc)
		case strings.HasPrefix(line, "? "):
			res.Changes = append(res.Changes, FileChange{
				Path: strings.TrimPrefix(line, "? "), WorkTree: "?",
				Unstaged: true, Untracked: true,
			})
		case strings.HasPrefix(line, "u "):
			// Unmerged (conflict). XY then path at the end.
			parts := strings.SplitN(line, " ", 11)
			if len(parts) == 11 {
				xy := parts[1]
				res.Changes = append(res.Changes, FileChange{
					Path: parts[10], Index: xy[:1], WorkTree: xy[1:],
					Staged: true, Unstaged: true,
				})
			}
		}
	}
	return res
}

// parseOrdinary decodes a "1 " (ordinary) or "2 " (rename) entry up to
// and including the path. Layout (space-separated):
//
//	1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>
//	2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <Xscore> <path>
func parseOrdinary(line string) FileChange {
	prefix := line[:2] // "1 " or "2 "
	rest := line[2:]
	rename := prefix == "2 "
	n := 8
	if rename {
		n = 9
	}
	parts := strings.SplitN(rest, " ", n)
	if len(parts) < n {
		return FileChange{Path: line}
	}
	xy := parts[0]
	path := parts[n-1]
	x, y := xy[:1], xy[1:]
	return FileChange{
		Path:     path,
		Index:    x,
		WorkTree: y,
		Staged:   x != "." && x != " ",
		Unstaged: y != "." && y != " ",
	}
}

// ── Diff ────────────────────────────────────────────────────────────

// Diff returns the unified diff for path. staged → `git diff --cached`.
// For untracked files git produces no diff; callers should read the
// file content instead.
func Diff(ctx context.Context, dir, path string, staged bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	args := []string{"diff", "--no-color"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	return run(ctx, dir, args...)
}

// FileAtRef returns the content of path at the given ref (e.g. "HEAD").
// Empty string + nil error when the file does not exist at that ref
// (newly added file) — the caller treats that as "no original side".
func FileAtRef(ctx context.Context, dir, ref, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	out, err := run(ctx, dir, "show", ref+":"+path)
	if err != nil {
		// `git show HEAD:missing` exits non-zero — treat as empty original
		// rather than an error so a newly-added file diffs against "".
		var ge *GitError
		if errors.As(err, &ge) && strings.Contains(ge.Stderr, "does not exist") {
			return "", nil
		}
		if errors.As(err, &ge) && strings.Contains(ge.Stderr, "exists on disk, but not in") {
			return "", nil
		}
		return "", err
	}
	return out, nil
}

// FileAtIndex returns the staged (index) content of path. Empty + nil
// when the path is not in the index. `git show :path` reads the index.
func FileAtIndex(ctx context.Context, dir, path string) (string, error) {
	return FileAtRef(ctx, dir, "", path)
}

// ── Branches ────────────────────────────────────────────────────────

// BranchList is the set of local + remote branches plus the current one.
type BranchList struct {
	Current  string   `json:"current"`
	Branches []string `json:"branches"`        // local
	Remotes  []string `json:"remotes"`         // remote-tracking (e.g. origin/main)
}

// Branches lists local + remote branches and marks the current local one.
func Branches(ctx context.Context, dir string) (BranchList, error) {
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	res := BranchList{Branches: []string{}, Remotes: []string{}}

	out, err := run(ctx, dir, "branch", "--format=%(HEAD)%(refname:short)")
	if err != nil {
		return BranchList{}, err
	}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" {
			continue
		}
		cur := strings.HasPrefix(ln, "*")
		name := strings.TrimSpace(strings.TrimPrefix(ln, "*"))
		if name == "" {
			continue
		}
		res.Branches = append(res.Branches, name)
		if cur {
			res.Current = name
		}
	}

	// Remote-tracking branches (skip the symbolic `origin/HEAD`).
	if rout, rerr := run(ctx, dir, "branch", "-r", "--format=%(refname:short)"); rerr == nil {
		for _, ln := range strings.Split(rout, "\n") {
			ln = strings.TrimSpace(strings.TrimRight(ln, "\r"))
			if ln == "" || strings.Contains(ln, "->") || strings.HasSuffix(ln, "/HEAD") {
				continue
			}
			res.Remotes = append(res.Remotes, ln)
		}
	}
	return res, nil
}

// ── Mutations ───────────────────────────────────────────────────────

// Stage runs `git add -- <paths>`.
func Stage(ctx context.Context, dir string, paths []string) error {
	if len(paths) == 0 {
		return errors.New("no paths to stage")
	}
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	args := append([]string{"add", "--"}, paths...)
	_, err := run(ctx, dir, args...)
	return err
}

// Unstage runs `git restore --staged -- <paths>`.
func Unstage(ctx context.Context, dir string, paths []string) error {
	if len(paths) == 0 {
		return errors.New("no paths to unstage")
	}
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	args := append([]string{"restore", "--staged", "--"}, paths...)
	_, err := run(ctx, dir, args...)
	return err
}

// Commit runs `git commit -m <msg>` and returns the new short SHA.
func Commit(ctx context.Context, dir, message string) (string, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return "", errors.New("commit message is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	if _, err := run(ctx, dir, "commit", "-m", message); err != nil {
		return "", err
	}
	out, err := run(ctx, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Discard reverts working-tree changes for the given paths — DESTRUCTIVE,
// no undo. For each path:
//   - untracked → removed from disk (git clean -fd <path>)
//   - tracked   → working + index restored to HEAD (git restore --staged
//     --worktree <path>), so both staged and unstaged edits are dropped.
// untrackedPaths must list which of paths are untracked (the caller knows
// from status) so we pick clean vs restore correctly.
func Discard(ctx context.Context, dir string, paths []string, untrackedPaths []string) error {
	if len(paths) == 0 {
		return errors.New("no paths to discard")
	}
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()

	untracked := map[string]bool{}
	for _, p := range untrackedPaths {
		untracked[p] = true
	}
	var tracked, toClean []string
	for _, p := range paths {
		if untracked[p] {
			toClean = append(toClean, p)
		} else {
			tracked = append(tracked, p)
		}
	}
	if len(tracked) > 0 {
		args := append([]string{"restore", "--staged", "--worktree", "--"}, tracked...)
		if _, err := run(ctx, dir, args...); err != nil {
			return err
		}
	}
	if len(toClean) > 0 {
		args := append([]string{"clean", "-fd", "--"}, toClean...)
		if _, err := run(ctx, dir, args...); err != nil {
			return err
		}
	}
	return nil
}

// Checkout switches to an existing branch.
func Checkout(ctx context.Context, dir, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("branch is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	_, err := run(ctx, dir, "checkout", branch)
	return err
}

// CreateBranch creates a branch. When checkout is true it switches to it.
func CreateBranch(ctx context.Context, dir, name string, checkout bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("branch name is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	if checkout {
		_, err := run(ctx, dir, "checkout", "-b", name)
		return err
	}
	_, err := run(ctx, dir, "branch", name)
	return err
}

// Push runs `git push`. stderr (auth, rejection) surfaces via GitError.
func Push(ctx context.Context, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, netTimeout)
	defer cancel()
	return run(ctx, dir, "push")
}

// Pull runs `git pull --ff-only`. Non-fast-forward surfaces as GitError.
func Pull(ctx context.Context, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, netTimeout)
	defer cancel()
	return run(ctx, dir, "pull", "--ff-only")
}

// ── File read/write (within repo) ───────────────────────────────────

// ReadFile reads a file at path (relative to repo root). The path is
// validated to stay inside dir.
func ReadFile(dir, path string) (string, error) {
	full, err := safeRepoJoin(dir, path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// WriteFile writes content to path (relative to repo root), creating
// parent dirs as needed. The path is validated to stay inside dir.
func WriteFile(dir, path, content string) error {
	full, err := safeRepoJoin(dir, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

// safeRepoJoin resolves rel against repoDir, rejecting traversal that
// escapes the repo. Mirrors the guard in tools/agents/context.go:safeJoin
// (absolute paths, "..", Windows volume names, NUL, symlink escape).
func safeRepoJoin(base, rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", errors.New("invalid path")
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	clean := filepath.Clean(rel)
	if clean == "." || clean == "" {
		return "", errors.New("path is empty")
	}
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, `\`) {
		return "", errors.New("absolute path not allowed")
	}
	if filepath.VolumeName(clean) != "" {
		return "", errors.New("volume-qualified path not allowed")
	}
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			return "", errors.New("path traversal not allowed")
		}
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	if resolved, rerr := filepath.EvalSymlinks(absBase); rerr == nil {
		absBase = resolved
	}
	absFull, err := filepath.Abs(filepath.Join(absBase, clean))
	if err != nil {
		return "", err
	}
	check := absFull
	for {
		if resolved, rerr := filepath.EvalSymlinks(check); rerr == nil {
			absFull = filepath.Join(resolved, strings.TrimPrefix(absFull, check))
			break
		}
		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}
	sep := string(filepath.Separator)
	if absFull != absBase && !strings.HasPrefix(absFull, absBase+sep) {
		return "", errors.New("path escapes repo")
	}
	return absFull, nil
}

// ── History ─────────────────────────────────────────────────────────

// LogEntry is one commit in the history list.
type LogEntry struct {
	SHA      string `json:"sha"`       // short sha
	Subject  string `json:"subject"`
	Author   string `json:"author"`
	RelDate  string `json:"rel_date"`  // e.g. "2 hours ago"
	ISODate  string `json:"iso_date"`
}

// logSep / logFieldSep are unlikely-to-collide delimiters for parsing
// `git log` output without quoting headaches.
const (
	logRecSep   = "\x1e" // record separator
	logFieldSep = "\x1f" // field separator
)

// Log returns up to limit recent commits on the current branch.
func Log(ctx context.Context, dir string, limit int) ([]LogEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	format := strings.Join([]string{"%h", "%s", "%an", "%cr", "%cI"}, logFieldSep) + logRecSep
	out, err := run(ctx, dir, "log", "--max-count="+strconv.Itoa(limit), "--pretty=format:"+format)
	if err != nil {
		return nil, err
	}
	var entries []LogEntry
	for _, rec := range strings.Split(out, logRecSep) {
		rec = strings.Trim(rec, "\n\r")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, logFieldSep)
		if len(f) < 5 {
			continue
		}
		entries = append(entries, LogEntry{
			SHA: f[0], Subject: f[1], Author: f[2], RelDate: f[3], ISODate: f[4],
		})
	}
	if entries == nil {
		entries = []LogEntry{}
	}
	return entries, nil
}

// CommitFile is one file changed in a commit.
type CommitFile struct {
	Path   string `json:"path"`
	Status string `json:"status"` // A/M/D/R...
}

// CommitDetail is a commit's metadata + changed-file list.
type CommitDetail struct {
	SHA     string       `json:"sha"`
	Subject string       `json:"subject"`
	Author  string       `json:"author"`
	ISODate string       `json:"iso_date"`
	Files   []CommitFile `json:"files"`
}

// CommitInfo returns metadata + the changed-file list for a commit
// (vs its first parent).
func CommitInfo(ctx context.Context, dir, sha string) (CommitDetail, error) {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return CommitDetail{}, errors.New("sha is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	// Header line (field-separated) then NUL-delimited name-status.
	format := strings.Join([]string{"%h", "%s", "%an", "%cI"}, logFieldSep)
	out, err := run(ctx, dir, "show", "--no-color", "--name-status", "-z", "--pretty=format:"+format+"%x00", sha)
	if err != nil {
		return CommitDetail{}, err
	}
	det := CommitDetail{Files: []CommitFile{}}
	// Split header from the name-status body at the first NUL.
	parts := strings.SplitN(out, "\x00", 2)
	hf := strings.Split(parts[0], logFieldSep)
	if len(hf) >= 4 {
		det.SHA, det.Subject, det.Author, det.ISODate = hf[0], hf[1], hf[2], hf[3]
	}
	if len(parts) == 2 {
		det.Files = parseNameStatusZ(parts[1])
	}
	return det, nil
}

// parseNameStatusZ decodes `--name-status -z` output. Each entry is a
// status field then the path (rename/copy carry two paths).
func parseNameStatusZ(s string) []CommitFile {
	fields := strings.Split(s, "\x00")
	out := []CommitFile{}
	for i := 0; i < len(fields); i++ {
		st := strings.TrimSpace(fields[i])
		if st == "" {
			continue
		}
		code := st[:1]
		// Rename/copy (R100, C75) consume the source path then the dest.
		if code == "R" || code == "C" {
			if i+2 < len(fields) {
				out = append(out, CommitFile{Path: fields[i+2], Status: code})
				i += 2
			}
			continue
		}
		if i+1 < len(fields) {
			out = append(out, CommitFile{Path: fields[i+1], Status: code})
			i++
		}
	}
	return out
}

// CommitFileDiff returns the unified diff for a single file in a commit
// (vs its first parent).
func CommitFileDiff(ctx context.Context, dir, sha, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	return run(ctx, dir, "show", "--no-color", "--format=", sha, "--", path)
}

// FileAtCommit returns a file's content at a specific commit. Empty when
// the file did not exist there.
func FileAtCommit(ctx context.Context, dir, sha, path string) (string, error) {
	return FileAtRef(ctx, dir, sha, path)
}
