package delegation

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// Handles address one INSTANCE, not a role.
//
// Two sub-agents spawned from the same profile must be separately
// reachable: the moment a second reviewer exists, "@reviewer" addressed
// to a role is ambiguous, and a message that lands on the wrong instance
// reads as an answer from an agent that never saw the question.

// handleUnsafe matches everything a handle may not contain. Handles are
// typed by a model inside prose, so they stay to a narrow alphabet that
// cannot collide with punctuation the model is likely to write next.
var handleUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// AllocateHandle returns a handle for a new instance of profileKey that
// collides with nothing in taken.
//
// Numbering never reuses a gap-free sequence position: it picks the
// lowest free suffix, so a tree that has lost "reviewer-2" gets it back
// rather than climbing forever.
func AllocateHandle(profileKey string, taken []string) string {
	base := handleUnsafe.ReplaceAllString(strings.ToLower(strings.TrimSpace(profileKey)), "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "agent"
	}
	used := make(map[string]bool, len(taken)+1)
	used[entity.LeaderHandle] = true
	for _, t := range taken {
		used[t] = true
	}
	if !used[base] {
		return base
	}
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s-%d", base, n)
		if !used[cand] {
			return cand
		}
	}
}
