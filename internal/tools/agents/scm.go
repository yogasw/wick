package agents

import (
	"context"
	"net/http"
	"strconv"

	"github.com/yogasw/wick/internal/agents/scm"
	"github.com/yogasw/wick/pkg/tool"
)

// registerSCM wires the git source-control JSON endpoints. All are
// session-scoped: the repo set is discovered under the session cwd
// (resolveSessionCwd) and a client may only address a repo by the Rel
// handle DiscoverRepos produced — never a raw path.
func registerSCM(r tool.Router) {
	r.GET("/api/sessions/{id}/git/repos", gitRepos)
	r.GET("/api/sessions/{id}/git/status", gitStatus)
	r.GET("/api/sessions/{id}/git/diff", gitDiff)
	r.GET("/api/sessions/{id}/git/file", gitReadFile)
	r.GET("/api/sessions/{id}/git/branches", gitBranches)
	r.GET("/api/sessions/{id}/git/blob", gitBlob)
	r.GET("/api/sessions/{id}/git/compare", gitCompare)
	r.GET("/api/sessions/{id}/git/log", gitLog)
	r.GET("/api/sessions/{id}/git/commit", gitCommitInfo)
	r.GET("/api/sessions/{id}/git/commit-diff", gitCommitDiff)
	r.POST("/api/sessions/{id}/git/stage", gitStage)
	r.POST("/api/sessions/{id}/git/unstage", gitUnstage)
	r.POST("/api/sessions/{id}/git/discard", gitDiscard)
	r.POST("/api/sessions/{id}/git/commit", gitCommit)
	r.POST("/api/sessions/{id}/git/branch/switch", gitBranchSwitch)
	r.POST("/api/sessions/{id}/git/branch/create", gitBranchCreate)
	r.POST("/api/sessions/{id}/git/push", gitPush)
	r.POST("/api/sessions/{id}/git/pull", gitPull)
	r.POST("/api/sessions/{id}/git/file", gitWriteFile)
}

// sessionCwd resolves the session id from the path and returns its cwd.
// Writes the error response and returns ok=false on failure.
func sessionCwd(c *tool.Ctx) (cwd string, ok bool) {
	// SCM only needs the registry + layout, not the agent pool — keep its
	// own guard so git ops work even when no pool is wired (e.g. tests).
	if globalMgr == nil {
		c.Error(http.StatusServiceUnavailable, "agents not initialised — check server boot logs")
		return "", false
	}
	id := c.PathValue("id")
	sess, found := globalMgr.Registry().Session(id)
	if !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return "", false
	}
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return "", false
	}
	return cwd, true
}

// repoDir resolves the ?repo= handle against the session cwd. Writes the
// error response and returns ok=false on failure.
func repoDir(c *tool.Ctx, cwd string) (dir string, ok bool) {
	rel := c.Query("repo")
	dir, err := scm.ResolveRepoDir(cwd, rel)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid repo: " + err.Error()})
		return "", false
	}
	return dir, true
}

// gitErr writes a git failure as a 400 with git's own message.
func gitErr(c *tool.Ctx, err error) {
	c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
}

// ── Read endpoints ──────────────────────────────────────────────────

// RepoSummary is one repo in the /git/repos listing.
type RepoSummary struct {
	Rel     string `json:"rel"`
	Name    string `json:"name"`
	Branch  string `json:"branch"`
	Changed int    `json:"changed"`
	Ahead   int    `json:"ahead"`
	Behind  int    `json:"behind"`
}

// GitStatusSnapshot is the full session-wide git state pushed over SSE
// (git_status event) AND returned by GET /git/repos. The FE renders
// entirely from this — repos for the switcher, statuses[rel] for the
// changes list of each repo — so a change event needs no follow-up
// fetch (zero polling).
type GitStatusSnapshot struct {
	Repos        []RepoSummary           `json:"repos"`
	Statuses     map[string]scm.StatusResult `json:"statuses"`
	TotalChanged int                     `json:"total_changed"`
}

// buildGitSnapshot scans cwd and computes the full snapshot. One
// `git status` per repo; bounded by repo count. Errors on a single repo
// are skipped so one broken repo doesn't blank the whole panel.
func buildGitSnapshot(ctx context.Context, cwd string) GitStatusSnapshot {
	snap := GitStatusSnapshot{Statuses: map[string]scm.StatusResult{}}
	repos, err := scm.DiscoverRepos(cwd)
	if err != nil {
		snap.Repos = []RepoSummary{}
		return snap
	}
	snap.Repos = make([]RepoSummary, 0, len(repos))
	for _, rp := range repos {
		dir, derr := scm.ResolveRepoDir(cwd, rp.Rel)
		if derr != nil {
			continue
		}
		s := RepoSummary{Rel: rp.Rel, Name: rp.Name}
		if st, serr := scm.Status(ctx, dir); serr == nil {
			s.Branch = st.Branch.Name
			s.Changed = len(st.Changes)
			s.Ahead = st.Branch.Ahead
			s.Behind = st.Branch.Behind
			snap.Statuses[rp.Rel] = st
			snap.TotalChanged += len(st.Changes)
		}
		snap.Repos = append(snap.Repos, s)
	}
	return snap
}

// gitRepos returns the full snapshot — repos + per-repo status — so the
// FE's initial load is a single request and every later update arrives
// via the git_status SSE event (same shape).
func gitRepos(c *tool.Ctx) {
	cwd, ok := sessionCwd(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, buildGitSnapshot(c.Context(), cwd))
}

func gitStatus(c *tool.Ctx) {
	cwd, ok := sessionCwd(c)
	if !ok {
		return
	}
	dir, ok := repoDir(c, cwd)
	if !ok {
		return
	}
	st, err := scm.Status(c.Context(), dir)
	if err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, st)
}

func gitDiff(c *tool.Ctx) {
	cwd, ok := sessionCwd(c)
	if !ok {
		return
	}
	dir, ok := repoDir(c, cwd)
	if !ok {
		return
	}
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	staged := c.Query("staged") == "1" || c.Query("staged") == "true"
	d, err := scm.Diff(c.Context(), dir, path, staged)
	if err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"diff": d})
}

func gitReadFile(c *tool.Ctx) {
	cwd, ok := sessionCwd(c)
	if !ok {
		return
	}
	dir, ok := repoDir(c, cwd)
	if !ok {
		return
	}
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	content, err := scm.ReadFile(dir, path)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"content": content, "path": path})
}

func gitBranches(c *tool.Ctx) {
	cwd, ok := sessionCwd(c)
	if !ok {
		return
	}
	dir, ok := repoDir(c, cwd)
	if !ok {
		return
	}
	bl, err := scm.Branches(c.Context(), dir)
	if err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, bl)
}

// gitBlob returns a file's content at a ref (default HEAD). Used by the
// FE to feed Monaco's diff editor the "original" side — Monaco computes
// the diff against the working copy, which is accurate (no unified-diff
// reconstruction).
func gitBlob(c *tool.Ctx) {
	cwd, ok := sessionCwd(c)
	if !ok {
		return
	}
	dir, ok := repoDir(c, cwd)
	if !ok {
		return
	}
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	ref := c.Query("ref")
	if ref == "" {
		ref = "HEAD"
	}
	content, err := scm.FileAtRef(c.Context(), dir, ref, path)
	if err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"content": content, "path": path, "ref": ref})
}

// gitCompare returns the two raw sides for a working-tree change, picked
// to match git semantics so STAGED and CHANGES show different diffs:
//   staged=true  → HEAD (original)  vs index   (modified)
//   staged=false → index (original) vs working (modified)
// The FE feeds both to Monaco, which computes + colors the diff.
func gitCompare(c *tool.Ctx) {
	cwd, ok := sessionCwd(c)
	if !ok {
		return
	}
	dir, ok := repoDir(c, cwd)
	if !ok {
		return
	}
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	staged := c.Query("staged") == "1" || c.Query("staged") == "true"
	untracked := c.Query("untracked") == "1" || c.Query("untracked") == "true"

	var original, modified string
	var err error
	switch {
	case untracked:
		// New file: nothing on the left, working content on the right.
		modified, err = scm.ReadFile(dir, path)
	case staged:
		original, err = scm.FileAtRef(c.Context(), dir, "HEAD", path)
		if err == nil {
			modified, err = scm.FileAtIndex(c.Context(), dir, path)
		}
	default:
		original, err = scm.FileAtIndex(c.Context(), dir, path)
		if err == nil {
			modified, err = scm.ReadFile(dir, path)
		}
	}
	if err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"original": original, "modified": modified, "path": path})
}

func gitLog(c *tool.Ctx) {
	cwd, ok := sessionCwd(c)
	if !ok {
		return
	}
	dir, ok := repoDir(c, cwd)
	if !ok {
		return
	}
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	entries, err := scm.Log(c.Context(), dir, limit)
	if err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"commits": entries})
}

func gitCommitInfo(c *tool.Ctx) {
	cwd, ok := sessionCwd(c)
	if !ok {
		return
	}
	dir, ok := repoDir(c, cwd)
	if !ok {
		return
	}
	sha := c.Query("sha")
	if sha == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "sha required"})
		return
	}
	det, err := scm.CommitInfo(c.Context(), dir, sha)
	if err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, det)
}

// gitCommitDiff returns the unified diff for one file in a commit, plus
// the HEAD-style raw sides (parent vs commit) so the FE can render it in
// the same Monaco diff editor it uses for working changes.
func gitCommitDiff(c *tool.Ctx) {
	cwd, ok := sessionCwd(c)
	if !ok {
		return
	}
	dir, ok := repoDir(c, cwd)
	if !ok {
		return
	}
	sha := c.Query("sha")
	path := c.Query("path")
	if sha == "" || path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "sha and path required"})
		return
	}
	original, _ := scm.FileAtCommit(c.Context(), dir, sha+"^", path) // parent side; empty if added
	modified, err := scm.FileAtCommit(c.Context(), dir, sha, path)
	if err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"original": original, "modified": modified, "path": path})
}

// ── Mutation endpoints ──────────────────────────────────────────────

type repoPathsReq struct {
	Repo  string   `json:"repo"`
	Paths []string `json:"paths"`
}

type repoMsgReq struct {
	Repo    string `json:"repo"`
	Message string `json:"message"`
}

type repoBranchReq struct {
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
	Checkout bool   `json:"checkout"`
}

type repoFileReq struct {
	Repo    string `json:"repo"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

type repoOnlyReq struct {
	Repo string `json:"repo"`
}

// resolveBodyRepo resolves a repo handle taken from a JSON body field.
func resolveBodyRepo(c *tool.Ctx, repo string) (dir string, ok bool) {
	cwd, ok := sessionCwd(c)
	if !ok {
		return "", false
	}
	dir, err := scm.ResolveRepoDir(cwd, repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid repo: " + err.Error()})
		return "", false
	}
	return dir, true
}

func gitStage(c *tool.Ctx) {
	var req repoPathsReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	dir, ok := resolveBodyRepo(c, req.Repo)
	if !ok {
		return
	}
	if err := scm.Stage(c.Context(), dir, req.Paths); err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "staged"})
}

func gitUnstage(c *tool.Ctx) {
	var req repoPathsReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	dir, ok := resolveBodyRepo(c, req.Repo)
	if !ok {
		return
	}
	if err := scm.Unstage(c.Context(), dir, req.Paths); err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "unstaged"})
}

type discardReq struct {
	Repo      string   `json:"repo"`
	Paths     []string `json:"paths"`
	Untracked []string `json:"untracked"` // which of Paths are untracked
}

// gitDiscard reverts working-tree changes (DESTRUCTIVE). The FE must
// confirm with the user first.
func gitDiscard(c *tool.Ctx) {
	var req discardReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	dir, ok := resolveBodyRepo(c, req.Repo)
	if !ok {
		return
	}
	if err := scm.Discard(c.Context(), dir, req.Paths, req.Untracked); err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "discarded"})
}

func gitCommit(c *tool.Ctx) {
	var req repoMsgReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	dir, ok := resolveBodyRepo(c, req.Repo)
	if !ok {
		return
	}
	sha, err := scm.Commit(c.Context(), dir, req.Message)
	if err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"sha": sha})
}

func gitBranchSwitch(c *tool.Ctx) {
	var req repoBranchReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	dir, ok := resolveBodyRepo(c, req.Repo)
	if !ok {
		return
	}
	if err := scm.Checkout(c.Context(), dir, req.Branch); err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "switched", "branch": req.Branch})
}

func gitBranchCreate(c *tool.Ctx) {
	var req repoBranchReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	dir, ok := resolveBodyRepo(c, req.Repo)
	if !ok {
		return
	}
	if err := scm.CreateBranch(c.Context(), dir, req.Branch, req.Checkout); err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "created", "branch": req.Branch})
}

func gitPush(c *tool.Ctx) {
	var req repoOnlyReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	dir, ok := resolveBodyRepo(c, req.Repo)
	if !ok {
		return
	}
	out, err := scm.Push(c.Context(), dir)
	if err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "pushed", "output": out})
}

func gitPull(c *tool.Ctx) {
	var req repoOnlyReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	dir, ok := resolveBodyRepo(c, req.Repo)
	if !ok {
		return
	}
	out, err := scm.Pull(c.Context(), dir)
	if err != nil {
		gitErr(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "pulled", "output": out})
}

func gitWriteFile(c *tool.Ctx) {
	var req repoFileReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	dir, ok := resolveBodyRepo(c, req.Repo)
	if !ok {
		return
	}
	if err := scm.WriteFile(dir, req.Path, req.Content); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "saved", "path": req.Path})
}
