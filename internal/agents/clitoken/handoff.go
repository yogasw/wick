package clitoken

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// A CLI token lives in memory, which is right until the process is
// replaced — and the single most likely moment for a script to report is
// exactly then. Deploys are when builds run: the job that was handed a
// token starts before the swap and finishes after it, and a successor that
// never issued that token answers 401. The report is lost to the very
// event it was reporting on.
//
// So the tokens travel with the handover, the same way the per-spawn MCP
// credentials do: the outgoing process writes its live grants next to the
// intake baton, the successor adopts them at boot and deletes the file.
// One boot's window, 0600 in wick's own data dir, and every grant keeps
// its original expiry — nothing is extended and an expired one is dropped
// rather than resurrected.
const handoffFileName = "cli-tokens.json"

func handoffPath(dir string) string { return filepath.Join(dir, handoffFileName) }

type persisted struct {
	Token     string    `json:"token"`
	SessionID string    `json:"session_id"`
	UserID    string    `json:"user_id"`
	Note      string    `json:"note"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SaveHandoff writes the still-valid grants for a successor to adopt.
// An empty set removes any previous file instead of leaving a stale one
// for the next boot to import.
func (s *Store) SaveHandoff(dir string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	now := s.now()
	s.mu.Lock()
	out := make([]persisted, 0, len(s.m))
	for _, g := range s.m {
		if !now.Before(g.ExpiresAt) {
			continue
		}
		out = append(out, persisted{
			Token: g.Token, SessionID: g.SessionID, UserID: g.UserID,
			Note: g.Note, ExpiresAt: g.ExpiresAt,
		})
	}
	s.mu.Unlock()

	path := handoffPath(dir)
	if len(out) == 0 {
		_ = os.Remove(path)
		return 0, nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return 0, err
	}
	return len(out), nil
}

// LoadHandoff adopts what a predecessor left and removes the file. Grants
// that expired in the meantime are dropped.
func (s *Store) LoadHandoff(dir string) int {
	if dir == "" {
		return 0
	}
	path := handoffPath(dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	// Removed whether or not it parses: a file that cannot be read is not
	// going to read better on the next boot, and leaving it would keep
	// re-importing whatever it holds.
	defer func() { _ = os.Remove(path) }()

	var in []persisted
	if err := json.Unmarshal(raw, &in); err != nil {
		return 0
	}
	now := s.now()
	n := 0
	s.mu.Lock()
	for _, p := range in {
		if p.Token == "" || p.SessionID == "" || !now.Before(p.ExpiresAt) {
			continue
		}
		s.m[p.Token] = Grant{
			Token: p.Token, SessionID: p.SessionID, UserID: p.UserID,
			Note: p.Note, ExpiresAt: p.ExpiresAt,
		}
		n++
	}
	s.mu.Unlock()
	return n
}
