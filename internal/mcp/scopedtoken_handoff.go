package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// A scoped token is minted per agent spawn and kept in memory, which is
// exactly right until the process is replaced. During a graceful upgrade the
// old process keeps running its agents while the successor takes the socket —
// so those agents' own MCP calls arrive at a process that never issued their
// token and get a 401. The turn keeps going with its wick tools silently
// broken, which is a worse outcome than the interruption the handover was
// avoiding.
//
// So the tokens travel with the handover: the outgoing process writes its live
// grants next to the intake baton, the successor reads them at boot and
// deletes the file. The window is one boot, the file is 0600 in wick's own
// data dir, and every grant keeps its original expiry — nothing is extended,
// and an expired grant is dropped rather than resurrected.
const handoffFileName = "mcp-scoped.json"

type persistedGrant struct {
	Token      string    `json:"token"`
	UserID     string    `json:"user_id"`
	TagIDs     []string  `json:"tag_ids"`
	Expires    time.Time `json:"expires"`
	StripAdmin bool      `json:"strip_admin"`
}

func handoffPath(dir string) string { return filepath.Join(dir, handoffFileName) }

// SaveHandoff writes the still-valid grants so a successor can honour them.
// An empty set removes any previous file rather than leaving a stale one
// behind for the next boot to import.
func (s *ScopedTokens) SaveHandoff(dir string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	now := s.now()
	s.mu.Lock()
	out := make([]persistedGrant, 0, len(s.m))
	for tok, g := range s.m {
		if !g.expires.IsZero() && now.After(g.expires) {
			continue
		}
		out = append(out, persistedGrant{
			Token:      tok,
			UserID:     g.userID,
			TagIDs:     g.tagIDs,
			Expires:    g.expires,
			StripAdmin: g.stripAdmin,
		})
	}
	s.mu.Unlock()

	path := handoffPath(dir)
	if len(out) == 0 {
		_ = os.Remove(path)
		return 0, nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return 0, fmt.Errorf("marshal scoped tokens: %w", err)
	}
	// 0600 and written whole: a partial file read by the successor would
	// revoke every agent's tools just as surely as no file at all.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return 0, fmt.Errorf("write scoped tokens: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("install scoped tokens: %w", err)
	}
	return len(out), nil
}

// LoadHandoff imports grants left by a predecessor and deletes the file. It is
// safe to call when there is nothing to import: no file means no predecessor,
// which is the normal case for a cold start.
//
// Returns how many grants were adopted.
func (s *ScopedTokens) LoadHandoff(dir string) int {
	if dir == "" {
		return 0
	}
	path := handoffPath(dir)
	b, err := os.ReadFile(path)
	// Remove first: whatever happens next, this file must not be read by a
	// later boot. These are credentials, and the process that owns them is
	// the one starting now.
	_ = os.Remove(path)
	if err != nil {
		return 0
	}
	var in []persistedGrant
	if err := json.Unmarshal(b, &in); err != nil {
		return 0
	}
	now := s.now()
	n := 0
	s.mu.Lock()
	for _, g := range in {
		if g.Token == "" || (!g.Expires.IsZero() && now.After(g.Expires)) {
			continue
		}
		s.m[g.Token] = scopedGrant{
			userID:     g.UserID,
			tagIDs:     g.TagIDs,
			expires:    g.Expires,
			stripAdmin: g.StripAdmin,
		}
		n++
	}
	s.mu.Unlock()
	return n
}
