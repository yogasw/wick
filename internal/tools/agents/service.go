package agents

import (
	"encoding/json"
	"path/filepath"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/workspace"
)

// loadFirstUserMessage returns the cached label from meta (in-memory, fast).
// Falls back to scanning conversation.jsonl for sessions created before label
// caching was introduced.
func loadFirstUserMessage(layout agentconfig.Layout, sessionID string, maxLen int) string {
	if globalMgr != nil {
		if sess, ok := globalMgr.Registry().Session(sessionID); ok && sess.Meta.Label != "" {
			r := []rune(sess.Meta.Label)
			if len(r) > maxLen {
				return string(r[:maxLen]) + "…"
			}
			return sess.Meta.Label
		}
	}
	// Legacy fallback: scan conversation.jsonl and backfill label.
	var result string
	storage.ReadJSONL(layout.SessionConversation(sessionID), func(line []byte) bool {
		var t store.ConversationTurn
		if json.Unmarshal(line, &t) != nil || t.Role != "user" || t.Text == "" {
			return true
		}
		r := []rune(strings.TrimSpace(t.Text))
		if len(r) > maxLen {
			result = string(r[:maxLen]) + "…"
		} else {
			result = string(r)
		}
		return false
	})
	// Backfill label to disk so next call hits the fast path.
	if result != "" && globalMgr != nil {
		if sess, ok := globalMgr.Registry().Session(sessionID); ok && sess.Meta.Label == "" {
			sess.Meta.Label = result
			_ = session.SaveMeta(layout, sessionID, sess.Meta)
		}
	}
	return result
}

// loadConversation reads all ConversationTurn entries from conversation.jsonl
// for the given session. Returns an empty slice when the file is missing.
func loadConversation(layout agentconfig.Layout, sessionID string) ([]store.ConversationTurn, error) {
	var turns []store.ConversationTurn
	err := storage.ReadJSONL(layout.SessionConversation(sessionID), func(line []byte) bool {
		var t store.ConversationTurn
		if json.Unmarshal(line, &t) == nil && t.Role != "" {
			turns = append(turns, t)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return turns, nil
}

// loadCommands reads raw lines from the SHARED commands.jsonl filtered
// by the session's workspace cwd. Stage 9 moved the audit log out of
// per-session files and into a single app-wide jsonl; we filter on
// the read side so the UI session-detail Commands tab still shows
// only what's relevant to that session.
//
// Filter: an entry matches a session when its WorkDir equals the
// session's resolved workspace path or sits under it (prefix match
// with separator boundary). Entries with empty WorkDir (failure
// modes from gate-side timeouts where cwd was never recorded) are
// dropped from per-session views.
func loadCommands(layout agentconfig.Layout, sessionID string) ([]string, error) {
	wsName := lookupSessionWorkspace(layout, sessionID)
	wsPath := ""
	if wsName != "" {
		if p, err := workspace.ResolvePath(layout, wsName); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				wsPath = filepath.Clean(abs)
			} else {
				wsPath = filepath.Clean(p)
			}
		}
	}
	app := gate.AppName()
	if app == "" {
		app = "wick"
	}
	var lines []string
	err := storage.ReadJSONL(gate.SharedCommandsPath(app), func(line []byte) bool {
		if wsPath == "" {
			lines = append(lines, string(line))
			return true
		}
		var e gate.Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return true
		}
		if e.SessionID != "" {
			if e.SessionID == sessionID {
				lines = append(lines, string(line))
			}
			return true
		}
		if e.WorkDir == "" {
			return true
		}
		ew := e.WorkDir
		if abs, err := filepath.Abs(ew); err == nil {
			ew = filepath.Clean(abs)
		} else {
			ew = filepath.Clean(ew)
		}
		if ew == wsPath || strings.HasPrefix(ew, wsPath+string(filepath.Separator)) {
			lines = append(lines, string(line))
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return lines, nil
}

// lookupSessionWorkspace returns the workspace name from a session's
// meta.json, or empty if the session has none / can't be loaded.
func lookupSessionWorkspace(layout agentconfig.Layout, sessionID string) string {
	if globalMgr == nil {
		return ""
	}
	s, ok := globalMgr.Registry().Sessions()[sessionID]
	if !ok {
		return ""
	}
	return s.Meta.Workspace
}
