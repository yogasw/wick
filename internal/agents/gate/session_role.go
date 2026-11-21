package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SessionRole is what the person chatting in one wick session may not do
// with the agent's native tools (By role access). wick writes it on every
// message a person sends and the gate hook reads it on every tool call, so
// a guest's turn runs without Bash or Write while the owner's turn in the
// same session keeps them.
type SessionRole struct {
	Role string   `json:"role"`
	Deny []string `json:"deny"`
}

// SessionEnv names the wick session in a spawn's environment, so the gate
// hook (a child of the provider) finds that session's SessionRole.
const SessionEnv = "WICK_GATE_SESSION"

// SessionRolePath is where the SessionRole of a wick session lives: under
// the shared gate dir, outside any workspace the agent can edit.
func SessionRolePath(appName, sessionID string) string {
	return filepath.Join(sharedGateDir(appName), "roles", filepath.Base(sessionID)+".json")
}

// WriteSessionRole stores r for sessionID; a role with nothing denied
// removes the file instead.
func WriteSessionRole(appName, sessionID string, r SessionRole) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	path := SessionRolePath(appName, sessionID)
	if len(r.Deny) == 0 {
		return RemoveSessionRole(appName, sessionID)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir session role dir: %w", err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write session role %s: %w", path, err)
	}
	return os.Rename(tmp, path)
}

// RemoveSessionRole drops sessionID's SessionRole; a missing file is fine.
func RemoveSessionRole(appName, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	err := os.Remove(SessionRolePath(appName, sessionID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// LoadSessionRole reads sessionID's SessionRole. ok is false when there is
// none (no limit). A file that exists but cannot be parsed is an error: the
// hook must then refuse, never fall back to no limit.
func LoadSessionRole(appName, sessionID string) (r SessionRole, ok bool, err error) {
	if strings.TrimSpace(sessionID) == "" {
		return SessionRole{}, false, nil
	}
	data, err := os.ReadFile(SessionRolePath(appName, sessionID))
	if os.IsNotExist(err) {
		return SessionRole{}, false, nil
	}
	if err != nil {
		return SessionRole{}, false, fmt.Errorf("read session role: %w", err)
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return SessionRole{}, false, fmt.Errorf("parse session role: %w", err)
	}
	return r, true, nil
}

// Denies reports whether r refuses tool. Names compare case-insensitively
// so a provider spelling "bash" still matches "Bash".
func (r SessionRole) Denies(tool string) bool {
	for _, d := range r.Deny {
		if strings.EqualFold(d, tool) {
			return true
		}
	}
	return false
}

// MergeDeny is the union of a and b, in a's order then b's new names.
func MergeDeny(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string(nil), a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
