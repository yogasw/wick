package agents

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentstore "github.com/yogasw/wick/internal/agents/store"
)

// classifyArtifactKind maps a filename to a UI render kind. image/pdf/html
// get inline previews + a fullscreen viewer; markdown/text get a fullscreen
// viewer (rendered / monospace) on top of download; everything else is a
// plain download card.
func classifyArtifactKind(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp", ".svg":
		return "image"
	case ".pdf":
		return "pdf"
	case ".html", ".htm":
		return "html"
	case ".md", ".markdown":
		return "markdown"
	}
	if textArtifactExts[ext] {
		return "text"
	}
	return "file"
}

// textArtifactExts are source/text types excluded when a file was only read
// (never written) this turn — reading them for context is not "producing" them.
var textArtifactExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true,
	".py": true, ".rb": true, ".rs": true, ".java": true, ".c": true, ".h": true,
	".cpp": true, ".cc": true, ".md": true, ".txt": true, ".json": true, ".yaml": true,
	".yml": true, ".toml": true, ".sh": true, ".sql": true, ".css": true, ".env": true,
	".ini": true, ".cfg": true, ".lock": true, ".xml": true, ".csv": true, ".log": true,
}

func isTextArtifactExt(name string) bool {
	return textArtifactExts[strings.ToLower(filepath.Ext(name))]
}

// resolveWithinCwd turns an absolute-or-relative path into a clean, forward-slash
// path relative to cwd. ok=false if it escapes cwd.
func resolveWithinCwd(cwd, p string) (string, bool) {
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(cwd, p)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(filepath.Clean(cwd), abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// largeEventReadCap bounds reading a Large trace-event payload just to recover
// a file_path. Bigger payloads (huge Write content) are skipped.
const largeEventReadCap = 256 * 1024

type toolFilePath struct {
	FilePath string `json:"file_path"`
}

// deriveArtifacts reads the turn's trace index and returns the displayable
// files it produced under cwd. Written/edited files of any type qualify;
// read-only files qualify only when non-text (e.g. a converted PNG).
func deriveArtifacts(layout agentconfig.Layout, sessionID, base, cwd string, turn agentstore.ConversationTurn) []agentstore.Artifact {
	if turn.Role != "assistant" || !turn.HasTrace {
		return nil
	}
	data, err := os.ReadFile(layout.SessionThinking(sessionID, turn.TurnID))
	if err != nil {
		return nil
	}
	var idx agentstore.TurnTraceIndex
	if json.Unmarshal(data, &idx) != nil {
		return nil
	}

	type cand struct {
		rel     string
		written bool
	}
	order := []string{}
	seen := map[string]*cand{}
	add := func(rel string, written bool) {
		if c, ok := seen[rel]; ok {
			c.written = c.written || written
			return
		}
		seen[rel] = &cand{rel: rel, written: written}
		order = append(order, rel)
	}

	for _, ev := range idx.Events {
		if ev.Type != "tool_use" {
			continue
		}
		written := ev.ToolName == "Write" || ev.ToolName == "Edit"
		if !written && ev.ToolName != "Read" {
			continue
		}
		raw := ev.ToolInput
		if raw == "" && ev.Large && ev.Size > 0 && ev.Size <= largeEventReadCap {
			if pb, perr := os.ReadFile(layout.SessionThinkingEvent(sessionID, turn.TurnID, ev.EventID)); perr == nil {
				var payload agentstore.TurnEventPayload
				if json.Unmarshal(pb, &payload) == nil {
					raw = payload.ToolInput
				}
			}
		}
		if raw == "" {
			continue
		}
		var tf toolFilePath
		if json.Unmarshal([]byte(raw), &tf) != nil || tf.FilePath == "" {
			continue
		}
		rel, ok := resolveWithinCwd(cwd, tf.FilePath)
		if !ok {
			continue
		}
		add(rel, written)
	}

	out := make([]agentstore.Artifact, 0, len(order))
	for _, rel := range order {
		c := seen[rel]
		name := filepath.Base(rel)
		if !c.written && isTextArtifactExt(name) {
			continue // read-only source file — not an artifact
		}
		info, err := os.Stat(filepath.Join(cwd, filepath.FromSlash(rel)))
		if err != nil || info.IsDir() {
			continue
		}
		q := "?path=" + url.QueryEscape(rel)
		out = append(out, agentstore.Artifact{
			Name:        name,
			Path:        rel,
			URL:         base + "/sessions/" + sessionID + "/files/raw" + q,
			DownloadURL: base + "/sessions/" + sessionID + "/files/download" + q,
			Kind:        classifyArtifactKind(name),
			MIME:        normalizeExtMIME(filepath.Ext(name)),
			Size:        info.Size(),
		})
	}
	return out
}

// attachArtifactsToTurns fills Artifacts on each assistant turn in place.
func attachArtifactsToTurns(layout agentconfig.Layout, sessionID, base, cwd string, turns []agentstore.ConversationTurn) {
	for i := range turns {
		if a := deriveArtifacts(layout, sessionID, base, cwd, turns[i]); len(a) > 0 {
			turns[i].Artifacts = a
			turns[i].HasArtifact = true
		}
	}
}
