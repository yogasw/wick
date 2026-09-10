package agents

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/pkg/tool"
)

// resolveSessionCwd returns the absolute working directory for a session
// (the same path the agent process spawns in). Mirrors pool.resolveCwd
// but read-only — no MkdirAll. Falls back to <SessionDir>/cwd when no
// project is bound.
func resolveSessionCwd(sess session.Session) (string, error) {
	id := sess.Meta.ProjectID
	if id != "" && project.Exists(globalLayout, id) {
		return project.ResolvePath(globalLayout, id)
	}
	return filepath.Join(globalLayout.SessionDir(sess.ID), "cwd"), nil
}

// safeJoin resolves rel against base, rejecting any traversal that
// escapes base. Defends against:
//   - absolute paths
//   - ".." segments
//   - Windows drive letters / UNC paths
//   - NUL bytes
//   - symlinks pointing outside base (resolved with EvalSymlinks when
//     the target exists; non-existent targets are checked lexically)
//
// Empty rel returns base.
func safeJoin(base, rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("invalid path")
	}
	// Normalize separators so Windows backslashes can't bypass the
	// segment check.
	rel = strings.ReplaceAll(rel, "\\", "/")
	clean := filepath.Clean(rel)
	if clean == "." || clean == "" {
		return base, nil
	}
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, `\`) {
		return "", fmt.Errorf("absolute path not allowed")
	}
	// Windows: reject "C:..." style volume-relative paths.
	if vol := filepath.VolumeName(clean); vol != "" {
		return "", fmt.Errorf("volume-qualified path not allowed")
	}
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			return "", fmt.Errorf("path traversal not allowed")
		}
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	// Resolve symlinks on the base so the prefix check holds even when
	// the workspace path itself goes through a link.
	if resolved, rerr := filepath.EvalSymlinks(absBase); rerr == nil {
		absBase = resolved
	}
	full := filepath.Join(absBase, clean)
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	// If the target (or any parent up to base) is a symlink, resolve
	// the deepest existing prefix and re-check.
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
		return "", fmt.Errorf("path escapes session cwd")
	}
	return absFull, nil
}

type contextFileEntry struct {
	Path  string `json:"path"` // relative to cwd, forward slashes
	Name  string `json:"name"` // basename
	Size  int64  `json:"size"` // bytes (0 for dirs)
	IsDir bool   `json:"isDir"`
	MTime int64  `json:"mtime"` // unix ms
}

// maxDirEntries caps ONE directory listing. It is a guard against a
// pathological folder, not a budget shared across the tree — see
// sessionContextList for why that distinction is the whole point.
const maxDirEntries = 5000

// sessionContextList returns the immediate children of ONE directory:
// the session cwd, or ?path= relative to it. Hidden dotfiles are
// included, skipWalkDirs are omitted, and symlinks are not followed.
//
// It used to walk the entire tree depth-first and cut the result off at
// 5000 entries. That is fine for a session holding one repository and
// silently wrong for one holding many: the budget is spent depth-first
// in alphabetical order, so the contents of the first few repos consume
// it and every later top-level entry is never sent at all. In a session
// with 55 clones the walk died inside `chatnshop/vendor`, and the panel
// showed 98 of 410 top-level entries — with no indication that the rest
// existed. The folder people actually wanted (`wick`, letter w) looked
// like it had never been cloned.
//
// One level at a time removes the shared budget: the cap now applies to
// a single directory, which no real folder reaches, and depth costs an
// extra request instead of everyone else's visibility.
func sessionContextList(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rel := strings.Trim(strings.ReplaceAll(c.Query("path"), "\\", "/"), "/")
	dir, err := safeJoin(cwd, rel)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	entries, truncated := readDirEntries(dir, rel)
	c.JSON(http.StatusOK, map[string]any{
		"cwd": cwd, "path": rel, "files": entries, "truncated": truncated,
	})
}

// readDirEntries lists one directory as context entries, with rel as the
// prefix their paths are reported under. A directory that cannot be read
// (deleted mid-browse, permissions) yields an empty list rather than an
// error: the panel keeps working and simply shows nothing inside.
func readDirEntries(dir, rel string) ([]contextFileEntry, bool) {
	entries := []contextFileEntry{}
	names, err := os.ReadDir(dir)
	if err != nil {
		return entries, false
	}
	truncated := false
	for _, de := range names {
		if de.IsDir() {
			if _, drop := skipWalkDirs[de.Name()]; drop {
				continue
			}
		}
		if len(entries) >= maxDirEntries {
			truncated = true
			break
		}
		info, ierr := de.Info()
		if ierr != nil {
			continue
		}
		p := de.Name()
		if rel != "" {
			p = rel + "/" + p
		}
		entries = append(entries, contextFileEntry{
			Path:  p,
			Name:  de.Name(),
			Size:  info.Size(),
			IsDir: de.IsDir(),
			MTime: info.ModTime().UnixMilli(),
		})
	}
	return entries, truncated
}

// sessionContextSearch backs the file panel's "Subfolders" search: a
// name substring across the whole tree, returning DIRECTORIES as well as
// files. GET /sessions/{id}/files/search?q=&limit=
//
// Separate from sessionContextMentions, which feeds the composer's
// @-mention and is deliberately files-only — you mention a file, not a
// folder. Here the folder is often the thing being looked for, and
// excluding it is what made typing "wick" in the panel return nothing
// useful.
//
// Ancestors of every hit are included so the client can attach the
// result to its tree without inventing the intermediate nodes.
func sessionContextSearch(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	q := strings.ToLower(strings.TrimSpace(c.Query("q")))
	if q == "" {
		c.JSON(http.StatusOK, map[string]any{"files": []contextFileEntry{}, "truncated": false})
		return
	}
	limit := 300
	if n, e := strconv.Atoi(c.Query("limit")); e == nil && n > 0 {
		limit = n
	}
	if limit > 2000 {
		limit = 2000
	}

	// maxVisit bounds the WALK, not the answer: a query that matches
	// early still returns promptly, and one that matches nothing stops
	// instead of touching every file in 55 checkouts.
	const maxVisit = 200000
	visited, truncated := 0, false
	seen := map[string]bool{}
	entries := []contextFileEntry{}
	add := func(rel string, info os.FileInfo) {
		if seen[rel] {
			return
		}
		seen[rel] = true
		entries = append(entries, contextFileEntry{
			Path:  rel,
			Name:  info.Name(),
			Size:  info.Size(),
			IsDir: info.IsDir(),
			MTime: info.ModTime().UnixMilli(),
		})
	}
	_ = filepath.Walk(cwd, func(p string, info os.FileInfo, err error) error {
		if err != nil || p == cwd {
			return nil
		}
		if info.IsDir() {
			if _, drop := skipWalkDirs[info.Name()]; drop {
				return filepath.SkipDir
			}
		}
		if len(entries) >= limit || visited >= maxVisit {
			truncated = true
			return filepath.SkipAll
		}
		visited++
		if !strings.Contains(strings.ToLower(info.Name()), q) {
			return nil
		}
		rel, rerr := filepath.Rel(cwd, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		// Ancestors first, so the client can build the branch top-down.
		for _, anc := range ancestorPaths(rel) {
			if ai, aerr := os.Stat(filepath.Join(cwd, anc)); aerr == nil {
				add(anc, ai)
			}
		}
		add(rel, info)
		return nil
	})
	c.JSON(http.StatusOK, map[string]any{"files": entries, "truncated": truncated})
}

// ancestorPaths lists the directory chain above a relative path, closest
// to the root first ("a/b/c.txt" → "a", "a/b").
func ancestorPaths(rel string) []string {
	segs := strings.Split(rel, "/")
	if len(segs) < 2 {
		return nil
	}
	out := make([]string, 0, len(segs)-1)
	for i := 1; i < len(segs); i++ {
		out = append(out, strings.Join(segs[:i], "/"))
	}
	return out
}

// sessionContextMentions walks the session cwd and returns FILE paths matching
// the space-separated AND terms in ?q= (every term must appear in the path),
// ranked best-first, capped at ?limit= (default 30, max 100). This backs the
// composer's @-mention search so it works over the whole tree — fresh on each
// keystroke and unaffected by the list endpoint's client-side cap. Dirs are
// excluded; an empty query returns the first files (a browsable default).
func sessionContextMentions(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Score the CACHED tree in memory rather than walking the disk per keystroke
	// — the walk is the expensive part, so caching it (short TTL) keeps rapid
	// @-search typing cheap. The frontend also debounces, so the two together
	// keep this endpoint from hanging on a large repo.
	c.JSON(http.StatusOK, map[string]any{
		"files": rankFilePaths(mentionTreeCache.paths(id, cwd), c.Query("q"), c.Query("limit")),
	})
}

// projectFileMentions mirrors sessionContextMentions but scopes the walk to a
// project's folder — used by the new-session composer's @-mention before any
// session exists (the session cwd IS the project folder once created).
// GET /api/projects/{id}/files/mentions. Access is enforced by projectAccessMW.
func projectFileMentions(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	if !project.Exists(globalLayout, id) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	cwd, err := project.ResolvePath(globalLayout, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"files": rankFilePaths(mentionTreeCache.paths("project:"+id, cwd), c.Query("q"), c.Query("limit")),
	})
}

// rankFilePaths filters paths to those matching the space-separated AND terms
// in q, ranks them (see scoreFilePath), and caps to limitStr (default 30, max
// 100). Shared by the session and project @-mention search endpoints.
func rankFilePaths(paths []string, q, limitStr string) []string {
	limit := 30
	if n, e := strconv.Atoi(limitStr); e == nil && n > 0 {
		limit = n
	}
	if limit > 100 {
		limit = 100
	}
	terms := strings.Fields(strings.ToLower(q))
	type scored struct {
		path  string
		score int
	}
	matches := []scored{}
	for _, rel := range paths {
		if s, matched := scoreFilePath(rel, terms); matched {
			matches = append(matches, scored{rel, s})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		return matches[i].path < matches[j].path
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.path)
	}
	return out
}

// scoreFilePath ranks a slash-separated path against AND search terms; lower is
// better. Returns (score, false) when any term is absent. Earlier hits, a
// basename match, and shorter paths rank higher — mirrors the composer's
// client-side fallback so results feel identical either way.
func scoreFilePath(path string, terms []string) (int, bool) {
	lp := strings.ToLower(path)
	score := 0
	for _, t := range terms {
		idx := strings.Index(lp, t)
		if idx == -1 {
			return 0, false
		}
		score += idx
	}
	if len(terms) > 0 {
		base := lp[strings.LastIndex(lp, "/")+1:]
		if strings.Contains(base, terms[len(terms)-1]) {
			score -= 1000
		}
	}
	return score + len(lp), true
}

// skipWalkDirs are noisy directories excluded from both the file list and the
// @-mention search walk.
var skipWalkDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, ".venv": {}, "venv": {},
	"__pycache__": {}, ".next": {}, "dist": {}, "build": {},
	"target": {}, ".cache": {},
}

// walkFilePaths returns slash-separated relative paths of every FILE under cwd
// (dirs excluded, skipWalkDirs pruned), capped at maxScan.
func walkFilePaths(cwd string) []string {
	// The cap is a runaway guard, not a budget anyone should hit. At 20000
	// it clipped a real session — 55 clones, 21605 files — so @-mentioning
	// anything in a repo late in the alphabet silently found nothing, the
	// same failure the file panel had. The walk itself costs ~70ms for that
	// tree and the result is cached for treeCacheTTL, so the old number was
	// buying nothing.
	const maxScan = 200000
	paths := make([]string, 0, 256)
	if _, err := os.Stat(cwd); err != nil {
		return paths
	}
	_ = filepath.Walk(cwd, func(p string, info os.FileInfo, err error) error {
		if err != nil || p == cwd {
			return nil
		}
		if info.IsDir() {
			if _, drop := skipWalkDirs[info.Name()]; drop {
				return filepath.SkipDir
			}
			return nil
		}
		if len(paths) >= maxScan {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(cwd, p)
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	return paths
}

// treeCache memoises the (expensive) file walk per session for a short TTL so
// the @-mention search scores an in-memory list instead of hitting the disk on
// every keystroke. New files still surface within one TTL.
type treeCache struct {
	mu    sync.Mutex
	items map[string]treeCacheEntry
}
type treeCacheEntry struct {
	paths   []string
	builtAt time.Time
}

const treeCacheTTL = 3 * time.Second

var mentionTreeCache = &treeCache{items: map[string]treeCacheEntry{}}

// paths returns the cached file list for a session, rebuilding it (off-lock) when
// stale. Stale entries for other sessions are evicted opportunistically so the
// map doesn't grow unbounded over a long-lived server.
func (c *treeCache) paths(sessionID, cwd string) []string {
	c.mu.Lock()
	if e, ok := c.items[sessionID]; ok && time.Since(e.builtAt) < treeCacheTTL {
		c.mu.Unlock()
		return e.paths
	}
	c.mu.Unlock()

	paths := walkFilePaths(cwd)

	c.mu.Lock()
	for id, e := range c.items {
		if id != sessionID && time.Since(e.builtAt) > 10*treeCacheTTL {
			delete(c.items, id)
		}
	}
	c.items[sessionID] = treeCacheEntry{paths: paths, builtAt: time.Now()}
	c.mu.Unlock()
	return paths
}

const maxReadBytes = 2 * 1024 * 1024 // 2 MiB

// sessionContextRead returns file contents as JSON. Binary files
// (any 0x00 byte in the first 8 KiB) are flagged via "binary":true
// so the client offers download instead of inline preview.
func sessionContextRead(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	rel := c.Query("path")
	if rel == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	full, err := safeJoin(cwd, rel)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	info, err := os.Stat(full)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if info.IsDir() {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "is a directory"})
		return
	}
	if info.Size() > maxReadBytes {
		c.JSON(http.StatusOK, map[string]any{
			"path":    filepath.ToSlash(rel),
			"size":    info.Size(),
			"tooBig":  true,
			"content": "",
		})
		return
	}
	data, err := os.ReadFile(full)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	bin := isBinary(data)
	resp := map[string]any{
		"path":   filepath.ToSlash(rel),
		"size":   info.Size(),
		"mtime":  info.ModTime().UnixMilli(),
		"binary": bin,
	}
	if !bin {
		resp["content"] = string(data)
	}
	c.JSON(http.StatusOK, resp)
}

func isBinary(b []byte) bool {
	n := len(b)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}

// sessionContextDownload streams the file with attachment headers.
func sessionContextDownload(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	rel := c.Query("path")
	if rel == "" {
		c.Error(http.StatusBadRequest, "path required")
		return
	}
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.Error(http.StatusNotFound, "session not found")
		return
	}
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	full, err := safeJoin(cwd, rel)
	if err != nil {
		c.Error(http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		c.Error(http.StatusNotFound, "not found")
		return
	}
	// Quote and strip CR/LF/quote chars to defend against header
	// injection via crafted filenames.
	safeName := strings.NewReplacer("\r", "", "\n", "", `"`, "", `\`, "").Replace(info.Name())
	c.W.Header().Set("Content-Disposition", `attachment; filename="`+safeName+`"`)
	http.ServeFile(c.W, c.R, full)
}

// artifactServeContentType returns the inline Content-Type for a name when the
// type is in the inline whitelist; inline=false means serve as a download.
func artifactServeContentType(name string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".svg" {
		return "image/svg+xml", true // safe via <img>; CSP sandbox added on serve
	}
	ct, ok := inlineSafeMIME[normalizeExtMIME(ext)]
	return ct, ok
}

// sessionContextRaw streams a cwd file inline when its type is whitelisted
// (images, pdf), else as a download. Used by artifact <img>/<iframe> previews.
func sessionContextRaw(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	rel := c.Query("path")
	if rel == "" {
		c.Error(http.StatusBadRequest, "path required")
		return
	}
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.Error(http.StatusNotFound, "session not found")
		return
	}
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	full, err := safeJoin(cwd, rel)
	if err != nil {
		c.Error(http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		c.Error(http.StatusNotFound, "not found")
		return
	}
	name := info.Name()
	c.W.Header().Set("X-Content-Type-Options", "nosniff")
	if ct, inline := artifactServeContentType(name); inline {
		c.W.Header().Set("Content-Type", ct)
		if ct == "image/svg+xml" {
			c.W.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		}
		c.W.Header().Set("Content-Disposition", `inline; filename="`+sanitizeHeaderFilename(name)+`"`)
	} else {
		c.W.Header().Set("Content-Type", "application/octet-stream")
		c.W.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeHeaderFilename(name)+`"`)
	}
	f, err := os.Open(full)
	if err != nil {
		c.Error(http.StatusInternalServerError, "open file")
		return
	}
	defer func() { _ = f.Close() }()
	http.ServeContent(c.W, c.R, name, info.ModTime(), f)
}

type contextSaveReq struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// sessionContextSave overwrites the file with new content. Creates
// parent dirs when missing.
func sessionContextSave(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	var req contextSaveReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	full, err := safeJoin(cwd, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := os.WriteFile(full, []byte(req.Content), 0o644); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	info, _ := os.Stat(full)
	c.JSON(http.StatusOK, map[string]any{
		"path":  filepath.ToSlash(req.Path),
		"size":  info.Size(),
		"mtime": info.ModTime().UnixMilli(),
		"ok":    true,
	})
}

type contextCreateReq struct {
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

// sessionContextCreate creates an empty file or a new directory inside
// the session cwd. Refuses if the target already exists.
func sessionContextCreate(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	var req contextCreateReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Path == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	full, err := safeJoin(cwd, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if _, err := os.Stat(full); err == nil {
		c.JSON(http.StatusConflict, map[string]string{"error": "already exists"})
		return
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if req.IsDir {
		if err := os.MkdirAll(full, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		_ = f.Close()
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "path": filepath.ToSlash(req.Path), "isDir": req.IsDir})
}

// sessionContextDelete removes a file or directory (recursive for dirs).
func sessionContextDelete(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	rel := c.Query("path")
	if rel == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	cwd, err := resolveSessionCwd(sess)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	full, err := safeJoin(cwd, rel)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if full == cwd {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot delete cwd"})
		return
	}
	if err := os.RemoveAll(full); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "ts": time.Now().UnixMilli()})
}
