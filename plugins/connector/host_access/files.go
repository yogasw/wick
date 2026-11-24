package main

import (
	"fmt"
	"github.com/yogasw/wick/pkg/connector"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// policyFor reads the saved configuration on each call so policy changes take effect without restarting the plugin.
func policyFor(c *connector.Ctx) (Policy, error) { return loadPolicy(c.Cfg("policy_json")) }

// fileAction centralizes authorization and serialization for native file handlers. Never register a file operation that bypasses this wrapper.
func fileAction(op string, fn func(Policy, string, *connector.Ctx) (any, error)) connector.ExecuteFunc {
	return func(c *connector.Ctx) (any, error) {
		p, err := policyFor(c)
		if err != nil {
			return nil, err
		}
		name, err := p.authorize(op, c.Input("path"))
		if err != nil {
			return nil, err
		}
		fileMu.Lock()
		defer fileMu.Unlock()
		if err = c.Context().Err(); err != nil {
			return nil, err
		}
		return fn(p, name, c)
	}
}

// fileRead returns bounded UTF-8 text and the full-file digest needed for optimistic edits.
func fileRead(_ Policy, name string, c *connector.Ctx) (any, error) {
	b, err := readFile(name)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
		return nil, fmt.Errorf("not a UTF-8 text file")
	}
	lines := strings.Split(string(b), "\n")
	start, end := c.InputInt("start_line"), c.InputInt("end_line")
	if start == 0 {
		start = 1
	}
	if start < 1 || start > len(lines) {
		return nil, fmt.Errorf("start_line out of range")
	}
	if end == 0 || end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return nil, fmt.Errorf("end_line before start_line")
	}
	if end-start >= 1000 {
		end = start + 999
	}
	return map[string]any{"path": name, "content": strings.Join(lines[start-1:end], "\n"), "start_line": start, "end_line": end, "total_lines": len(lines), "sha256": digest(b), "bytes": len(b)}, nil
}

// fileWrite replaces an existing file only when the caller supplies the expected digest.
func fileWrite(_ Policy, name string, c *connector.Ctx) (any, error) {
	b := []byte(c.Input("content"))
	if err := replaceFile(name, b, c.Input("expected_sha256")); err != nil {
		return nil, err
	}
	return map[string]any{"path": name, "sha256": digest(b), "bytes": len(b)}, nil
}

// fileCreate never overwrites a file that already exists.
func fileCreate(_ Policy, name string, c *connector.Ctx) (any, error) {
	b := []byte(c.Input("content"))
	if err := writeNew(name, b); err != nil {
		return nil, err
	}
	return map[string]any{"path": name, "sha256": digest(b), "bytes": len(b)}, nil
}

// fileEdit requires a unique exact match, avoiding ambiguous replacements in source files.
func fileEdit(p Policy, name string, c *connector.Ctx) (any, error) {
	if _, err := p.authorize("read", name); err != nil {
		return nil, err
	}
	b, err := readFile(name)
	if err != nil {
		return nil, err
	}
	expected := c.Input("expected_sha256")
	if expected == "" || digest(b) != expected {
		return nil, fmt.Errorf("expected_sha256 mismatch")
	}
	old, newText := c.Input("old_text"), c.Input("new_text")
	if old == "" || strings.Count(string(b), old) != 1 {
		return nil, fmt.Errorf("old_text must match exactly once")
	}
	updated := []byte(strings.Replace(string(b), old, newText, 1))
	if err := replaceFile(name, updated, expected); err != nil {
		return nil, err
	}
	return map[string]any{"path": name, "sha256": digest(updated), "bytes": len(updated)}, nil
}

// fileAppend requires read and write permission because it preserves existing content.
func fileAppend(p Policy, name string, c *connector.Ctx) (any, error) {
	if _, err := p.authorize("read", name); err != nil {
		return nil, err
	}
	b, err := readFile(name)
	if err != nil {
		return nil, err
	}
	b = append(b, []byte(c.Input("content"))...)
	if err := replaceFile(name, b, c.Input("expected_sha256")); err != nil {
		return nil, err
	}
	return map[string]any{"path": name, "sha256": digest(b), "bytes": len(b)}, nil
}

// fileCopy checks both source and destination permissions before creating a new file.
func fileCopy(p Policy, name string, c *connector.Ctx) (any, error) {
	if _, err := p.authorize("read", name); err != nil {
		return nil, err
	}
	dest, err := p.authorize("copy", c.Input("destination"))
	if err != nil {
		return nil, err
	}
	if _, err = p.authorize("create", dest); err != nil {
		return nil, err
	}
	b, err := readFile(name)
	if err != nil {
		return nil, err
	}
	if err = writeNew(dest, b); err != nil {
		return nil, err
	}
	return map[string]any{"path": name, "destination": dest, "sha256": digest(b), "copied": true}, nil
}

// Moving a file needs both source-delete and destination-create permissions.
func fileMove(p Policy, name string, c *connector.Ctx) (any, error) {
	for _, op := range []string{"read", "delete"} {
		if _, err := p.authorize(op, name); err != nil {
			return nil, err
		}
	}
	dest, err := p.authorize("move", c.Input("destination"))
	if err != nil {
		return nil, err
	}
	if _, err := p.authorize("create", dest); err != nil {
		return nil, err
	}
	if err := moveFile(name, dest, c.Input("expected_sha256")); err != nil {
		return nil, err
	}
	return map[string]any{"path": name, "destination": dest, "moved": true}, nil
}

// fileList filters every child independently; a readable parent does not imply that all its children are visible.
func fileList(p Policy, name string, c *connector.Ctx) (any, error) {
	limit := c.InputInt("limit")
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	names, more, err := listDir(name, limit)
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for _, n := range names {
		child := filepath.Join(name, n)
		// No protected path names or metadata leak through directory listing.
		if v, _ := p.fileVerdict("list", child); v != "allow" {
			continue
		}
		row := map[string]any{"name": n, "path": child}
		if v, _ := p.fileVerdict("stat", child); v == "allow" {
			s, e := statFile(child)
			if e != nil {
				continue
			}
			row = s
			row["name"] = n
		}
		rows = append(rows, row)
	}
	return map[string]any{"path": name, "entries": rows, "truncated": more}, nil
}

// search walks permitted directories with bounded depth, entries, bytes and results. It does not follow links or invoke an external grep process.
func search(p Policy, name string, c *connector.Ctx, content bool, tree bool) (any, error) {
	limit := c.InputInt("limit")
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	depth := c.InputInt("max_depth")
	if depth <= 0 || depth > 20 {
		depth = 8
	}
	pattern := c.Input("pattern")
	if pattern == "" {
		pattern = "*"
	}
	if _, err := filepath.Match(pattern, "x"); err != nil {
		return nil, err
	}
	query := c.Input("query")
	if content && query == "" {
		return nil, fmt.Errorf("query required")
	}
	rows := []map[string]any{}
	scanned := 0
	bytesRead := 0
	truncated := false
	var walk func(string, int)
	walk = func(dir string, d int) {
		if d > depth || scanned >= 5000 || len(rows) >= limit || c.Context().Err() != nil {
			truncated = true
			return
		}
		for _, op := range []string{"list", "search"} {
			if v, _ := p.fileVerdict(op, dir); v != "allow" {
				return
			}
		}
		names, more, err := listDir(dir, 5000-scanned)
		if err != nil {
			return
		}
		if more {
			truncated = true
		}
		for _, n := range names {
			scanned++
			if scanned > 5000 || len(rows) >= limit || c.Context().Err() != nil {
				truncated = true
				return
			}
			child := filepath.Join(dir, n)
			if v, _ := p.fileVerdict("search", child); v != "allow" {
				continue
			}
			if v, _ := p.fileVerdict("stat", child); v != "allow" {
				continue
			}
			st, e := statFile(child)
			if e != nil {
				continue
			}
			isDir := st["type"] == "directory"
			matched, _ := filepath.Match(pattern, n)
			if tree || (!content && matched) {
				rows = append(rows, map[string]any{"path": child, "type": st["type"]})
			}
			if isDir {
				walk(child, d+1)
				continue
			}
			if content && matched {
				if v, _ := p.fileVerdict("read", child); v != "allow" {
					continue
				}
				if bytesRead >= 16<<20 {
					truncated = true
					return
				}
				b, e := readFile(child)
				if e != nil {
					continue
				}
				bytesRead += len(b)
				if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
					continue
				}
				for i, line := range strings.Split(string(b), "\n") {
					if strings.Contains(line, query) {
						if len(line) > 1000 {
							line = line[:1000]
						}
						rows = append(rows, map[string]any{"path": child, "line": i + 1, "text": line})
						if len(rows) >= limit {
							truncated = true
							break
						}
					}
				}
			}
		}
	}
	if _, _, err := listDir(name, 1); err != nil {
		return nil, err
	}
	walk(name, 0)
	return map[string]any{"matches": rows, "scanned": scanned, "truncated": truncated}, nil
}

// systemInfo reports actual platform capability, not an assumed sandbox.
func systemInfo(c *connector.Ctx) (any, error) {
	p, err := policyFor(c)
	if err != nil {
		return nil, err
	}
	if !contains(p.System.Allow, "system_info") {
		return nil, fmt.Errorf("system_info denied")
	}
	host, _ := os.Hostname()
	return map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU(), "hostname": host, "secure_filesystem": runtime.GOOS == "linux" || runtime.GOOS == "android", "terminal_sandbox": false}, nil
}
