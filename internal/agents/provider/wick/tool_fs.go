package wick

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/genai"
)

// tool_fs.go provides native filesystem tools — read_file, write_file,
// edit_file — that manipulate files directly via Go's os package rather
// than through the shell tool. This is deliberate: the shell tool is
// gated by the command whitelist (gate.Matcher), which blocks shell
// metacharacters like redirects (`>`, `>>`) outright — so "create a file"
// via `echo ... > file` can never pass the gate, whitelist or not. These
// tools bypass that gate entirely (they are not shell commands, so
// gate.go's gateCheckerFn — which only inspects the "shell" tool name —
// never sees them) while still being confined to the session workspace
// via fsResolvePath, so the agent can't read/write outside its worktree.
//
// Enable/disable via WickConfigResolved.FsTools (default true, same as
// shell/todo) — see buildTools.

// fsMaxReadBytes caps how much of a file read_file returns in one call,
// matching the shell tool's output cap so a huge file can't blow the
// context budget.
const fsMaxReadBytes = 200_000

// fsResolvePath resolves a (possibly relative) path against the session
// workspace and rejects anything that would escape it. Write path — the
// workspace is the only allowed root.
func fsResolvePath(workspace, path string) (string, error) {
	return fsResolvePathIn(path, workspace, workspace)
}

// fsResolvePathReadable resolves a path allowed to live in the workspace
// OR the session's uploads dir. Files the user attaches land in
// <SessionDir>/uploads (outside the workspace), and the pool tells the
// agent to read them by absolute path via the "[Attached files]" block —
// so read_file MUST accept that root, or the agent is told to read a path
// its own tool rejects ("escapes the session workspace"). Read-only: the
// uploads dir is never a write target (see fsResolvePath).
func fsResolvePathReadable(workspace, uploadsDir, path string) (string, error) {
	return fsResolvePathIn(path, workspace, workspace, uploadsDir)
}

// fsResolvePathIn resolves path (relative → against relBase) and confirms
// the cleaned result stays within at least one of the allowed roots. Empty
// roots are skipped; when no non-empty root is configured (tests /
// headless) the path is returned as-is. A relative path resolves against
// relBase (the workspace).
func fsResolvePathIn(path, relBase string, roots ...string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	// Collect the non-empty roots we'll check against.
	var clean []string
	for _, r := range roots {
		if strings.TrimSpace(r) != "" {
			clean = append(clean, filepath.Clean(r))
		}
	}
	if len(clean) == 0 {
		// No scoping configured — allow as-is (callers that care set roots).
		return path, nil
	}

	var full string
	if filepath.IsAbs(path) {
		full = filepath.Clean(path)
	} else {
		base := relBase
		if strings.TrimSpace(base) == "" {
			base = clean[0]
		}
		full = filepath.Clean(filepath.Join(base, path))
	}
	for _, root := range clean {
		if full == root || strings.HasPrefix(full, root+string(filepath.Separator)) {
			return full, nil
		}
	}
	return "", fmt.Errorf("path %q escapes the session workspace", path)
}

// sessionUploadsDir is where user-attached files live for this spawn:
// <SessionDir>/uploads. Empty when SessionDir isn't wired (tests). Must
// match uploadsDirName in internal/tools/agents/uploads.go.
func sessionUploadsDir(tc toolContext) string {
	if tc.SessionDir == "" {
		return ""
	}
	return filepath.Join(tc.SessionDir, "uploads")
}

func readFileTool(tc toolContext) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{
			Name: "read_file",
			Description: "Read a text file's contents from the session workspace. Path may be " +
				"relative to the workspace or absolute (must stay inside the workspace). " +
				"Optionally pass start_line/end_line (1-indexed, inclusive) to read only a " +
				"slice of a large file instead of the whole thing — output is prefixed with " +
				"line numbers in that case so you can target edit_file's line-range mode. " +
				"Output is truncated if very long.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"path":       {Type: genai.TypeString, Description: "File path to read."},
					"start_line": {Type: genai.TypeInteger, Description: "First line to read, 1-indexed (optional)."},
					"end_line":   {Type: genai.TypeInteger, Description: "Last line to read, inclusive (optional, defaults to end of file)."},
				},
				Required: []string{"path"},
			},
		},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			p, _ := args["path"].(string)
			full, err := fsResolvePathReadable(tc.Workspace, sessionUploadsDir(tc), p)
			if err != nil {
				return "error: " + err.Error(), true
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return fmt.Sprintf("error: %v", err), true
			}
			body := string(data)

			startLine, hasStart := intArg(args, "start_line")
			endLine, hasEnd := intArg(args, "end_line")
			if hasStart || hasEnd {
				lines := strings.Split(body, "\n")
				start := 1
				if hasStart {
					start = startLine
				}
				end := len(lines)
				if hasEnd {
					end = endLine
				}
				if start < 1 {
					start = 1
				}
				if end > len(lines) {
					end = len(lines)
				}
				if start > end || start > len(lines) {
					return fmt.Sprintf("error: start_line %d is out of range (file has %d lines)", start, len(lines)), true
				}
				var sb strings.Builder
				for i := start; i <= end; i++ {
					fmt.Fprintf(&sb, "%d\t%s\n", i, lines[i-1])
				}
				body = sb.String()
			}

			if len(body) > fsMaxReadBytes {
				body = body[:fsMaxReadBytes] + "\n...(truncated)"
			}
			if body == "" {
				body = "(empty file)"
			}
			return body, false
		},
	}
}

// intArg reads an integer argument that may arrive as float64 (JSON) or
// int (tests), returning ok=false if absent or not a number.
func intArg(args map[string]any, key string) (int, bool) {
	v, present := args[key]
	if !present {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	default:
		return 0, false
	}
}

func writeFileTool(tc toolContext) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{
			Name: "write_file",
			Description: "Create or overwrite a file in the session workspace with the given " +
				"content. Creates parent directories as needed. Path may be relative to the " +
				"workspace or absolute (must stay inside the workspace).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"path":    {Type: genai.TypeString, Description: "File path to write."},
					"content": {Type: genai.TypeString, Description: "Full content to write (replaces any existing content)."},
				},
				Required: []string{"path", "content"},
			},
		},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			p, _ := args["path"].(string)
			content, _ := args["content"].(string)
			full, err := fsResolvePath(tc.Workspace, p)
			if err != nil {
				return "error: " + err.Error(), true
			}
			if dir := filepath.Dir(full); dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return fmt.Sprintf("error: creating parent dirs: %v", err), true
				}
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				return fmt.Sprintf("error: %v", err), true
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(content), p), false
		},
	}
}

func editFileTool(tc toolContext) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{
			Name: "edit_file",
			Description: "Edit an existing file, in one of two mutually exclusive modes. " +
				"Snippet mode (old_str/new_str): old_str must match exactly once in the file, " +
				"or the edit is rejected — use read_file first to see the current content and " +
				"avoid ambiguous matches. Line-range mode (start_line/end_line/new_str): " +
				"replaces lines start_line..end_line (1-indexed, inclusive) with new_str, " +
				"without needing to quote the file's existing content — use read_file's line " +
				"numbers to target it. Prefer line-range mode on large files.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"path":       {Type: genai.TypeString, Description: "File path to edit."},
					"old_str":    {Type: genai.TypeString, Description: "Snippet mode: exact text to find (must be unique in the file)."},
					"new_str":    {Type: genai.TypeString, Description: "Replacement text (both modes)."},
					"start_line": {Type: genai.TypeInteger, Description: "Line-range mode: first line to replace, 1-indexed."},
					"end_line":   {Type: genai.TypeInteger, Description: "Line-range mode: last line to replace, inclusive."},
				},
				Required: []string{"path", "new_str"},
			},
		},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			p, _ := args["path"].(string)
			oldStr, _ := args["old_str"].(string)
			newStr, _ := args["new_str"].(string)
			startLine, hasStart := intArg(args, "start_line")
			endLine, hasEnd := intArg(args, "end_line")

			full, err := fsResolvePath(tc.Workspace, p)
			if err != nil {
				return "error: " + err.Error(), true
			}

			if hasStart || hasEnd {
				if oldStr != "" {
					return "error: pass either old_str or start_line/end_line, not both", true
				}
				if !hasStart || !hasEnd {
					return "error: line-range mode requires both start_line and end_line", true
				}
				return editFileLineRange(full, p, startLine, endLine, newStr)
			}

			if oldStr == "" {
				return "error: old_str (or start_line/end_line) is required", true
			}
			return editFileSnippet(full, p, oldStr, newStr)
		},
	}
}

func editFileSnippet(full, displayPath, oldStr, newStr string) (string, bool) {
	data, err := os.ReadFile(full)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	body := string(data)
	n := strings.Count(body, oldStr)
	switch n {
	case 0:
		return "error: old_str not found in file", true
	case 1:
		// exactly one match — proceed
	default:
		return fmt.Sprintf("error: old_str matches %d times — must be unique, add more surrounding context", n), true
	}
	updated := strings.Replace(body, oldStr, newStr, 1)
	if err := os.WriteFile(full, []byte(updated), 0o644); err != nil {
		return fmt.Sprintf("error: writing updated file: %v", err), true
	}
	return fmt.Sprintf("edited %s (%d bytes -> %d bytes)", displayPath, len(body), len(updated)), false
}

func editFileLineRange(full, displayPath string, startLine, endLine int, newStr string) (string, bool) {
	data, err := os.ReadFile(full)
	if err != nil {
		return fmt.Sprintf("error: %v", err), true
	}
	body := string(data)
	lines := strings.Split(body, "\n")
	if startLine < 1 || endLine < startLine || startLine > len(lines) {
		return fmt.Sprintf("error: line range %d-%d is out of bounds (file has %d lines)", startLine, endLine, len(lines)), true
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	replacement := strings.Split(newStr, "\n")
	updatedLines := append([]string{}, lines[:startLine-1]...)
	updatedLines = append(updatedLines, replacement...)
	updatedLines = append(updatedLines, lines[endLine:]...)
	updated := strings.Join(updatedLines, "\n")
	if err := os.WriteFile(full, []byte(updated), 0o644); err != nil {
		return fmt.Sprintf("error: writing updated file: %v", err), true
	}
	return fmt.Sprintf("edited %s lines %d-%d (%d bytes -> %d bytes)", displayPath, startLine, endLine, len(body), len(updated)), false
}
