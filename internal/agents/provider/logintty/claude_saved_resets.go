package logintty

// Claude saved resets ("/limit-reset" grants).
//
// The OAuth usage endpoint carries them when asked with
// ?cedar_ember=1, so readClaudeUsage asks for them on the request it
// already makes and parks the parsed block here. The registered reader
// only hands that back: it never calls the endpoint itself, so saved
// resets cost no request of their own on a rate-limited host.

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/savedresets"
)

// Claude's reader registers itself; nothing outside this file knows it.
func init() {
	savedresets.Register(string(provider.TypeClaude), readClaudeSavedResets)
}

// claudeSavedResetsMaxAge bounds how old a parked reading may be when the
// reader picks it up. The reader runs right after the usage call in the
// same probe flight, so anything older belongs to an earlier probe.
const claudeSavedResetsMaxAge = 2 * time.Minute

type claudeSavedResetsMemo struct {
	at     time.Time
	resets *savedresets.SavedResets
}

var (
	claudeSavedResetsMu    sync.Mutex
	claudeSavedResetsByDir = map[string]claudeSavedResetsMemo{}
)

// errNoClaudeSavedResets: the last usage answer carried no cedar_ember
// block (or there was no usage answer yet). The section is then hidden.
var errNoClaudeSavedResets = errors.New("no saved-resets reading")

// claudeCedarEmber is the subset of the cedar_ember block wick reads.
type claudeCedarEmber struct {
	Eligible         *bool  `json:"eligible"`
	IneligibleReason string `json:"ineligible_reason"`
	Grants           []struct {
		ID               string `json:"id"`
		Label            string `json:"label"`
		ResetsTotal      int    `json:"resets_total"`
		ResetsLeft       int    `json:"resets_left"`
		StartsAt         any    `json:"starts_at"`
		EndsAt           any    `json:"ends_at"`
		UsableNow        bool   `json:"usable_now"`
		UseRequiresLimit bool   `json:"use_requires_limit"`
	} `json:"grants"`
	CooldownUntil any `json:"cooldown_until"`
}

const claudeSavedResetsHint = "Refills your limits; the weekly reset day stays the same. Use it with /limit-reset in Claude Code."

// parseClaudeSavedResets normalises a cedar_ember block; nil when the
// block is absent or unreadable.
func parseClaudeSavedResets(raw json.RawMessage) *savedresets.SavedResets {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var ce claudeCedarEmber
	if json.Unmarshal(raw, &ce) != nil {
		return nil
	}
	out := &savedresets.SavedResets{Supported: true, Hint: claudeSavedResetsHint}
	if ce.Eligible != nil && !*ce.Eligible {
		out.Note = "Not available for this account."
		out.Hint = ""
		return out
	}
	for _, g := range ce.Grants {
		out.Available += max(g.ResetsLeft, 0)
		out.Total += max(g.ResetsTotal, 0)
		if g.ResetsLeft <= 0 {
			continue
		}
		out.Items = append(out.Items, savedresets.Reset{
			ID:            g.ID,
			Label:         g.Label,
			StartsAt:      savedresets.ParseTime(g.StartsAt),
			ExpiresAt:     savedresets.ParseTime(g.EndsAt),
			UsableNow:     g.UsableNow,
			RequiresLimit: g.UseRequiresLimit,
		})
	}
	out.CooldownUntil = savedresets.ParseTime(ce.CooldownUntil)
	savedresets.SortItems(out.Items)
	return out
}

// rememberClaudeSavedResets parks the cedar_ember block of a usage answer
// for the reader. A missing block clears the memo, so a stale reading is
// never served after the account stops reporting one.
func rememberClaudeSavedResets(dir string, raw json.RawMessage, now time.Time) {
	claudeSavedResetsMu.Lock()
	defer claudeSavedResetsMu.Unlock()
	r := parseClaudeSavedResets(raw)
	if r == nil {
		delete(claudeSavedResetsByDir, dir)
		return
	}
	claudeSavedResetsByDir[dir] = claudeSavedResetsMemo{at: now, resets: r}
}

// readClaudeSavedResets is Claude's registered savedresets.Reader.
func readClaudeSavedResets(env []string) (*savedresets.SavedResets, error) {
	dir := claudeConfigDir(env)
	claudeSavedResetsMu.Lock()
	defer claudeSavedResetsMu.Unlock()
	m, ok := claudeSavedResetsByDir[dir]
	if !ok || time.Since(m.at) > claudeSavedResetsMaxAge {
		return nil, errNoClaudeSavedResets
	}
	return m.resets, nil
}
