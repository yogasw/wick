package agents

import (
	"net/http"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/skillsync"
	"github.com/yogasw/wick/pkg/tool"
)

// ComposerCommand is one entry in the composer's `/` menu. It is intentionally
// data-only: the FE maps Action ids to local handlers (opening a panel,
// switching provider, changing the view) — those live in the FE because they
// are UI actions. Skills carry no Action; the FE inserts `/`+Insert instead.
//
// To add a new `/` command that reuses an existing FE action, add an entry here
// with a known Action id — no FE change needed. A genuinely new behaviour needs
// one new handler in the FE action map.
type ComposerCommand struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Hint     string `json:"hint,omitempty"`
	Category string `json:"category,omitempty"`
	// Action is an id the FE resolves to a handler (e.g. "switch:provider",
	// "panel:process", "view:commands"). Empty for insert-type entries.
	Action string `json:"action,omitempty"`
	// Insert is the text placed after `/` for insert-type entries (skills).
	Insert string `json:"insert,omitempty"`
}

// ComposerCommandsResponse is the envelope for GET /api/composer/commands.
type ComposerCommandsResponse struct {
	Commands []ComposerCommand `json:"commands"`
}

// builtinComposerCommands are the static `/` actions, grouped by category. The
// FE renders them in this order and resolves Action ids to handlers.
var builtinComposerCommands = []ComposerCommand{
	{ID: "provider", Label: "/provider", Hint: "switch provider", Category: "Switch", Action: "switch:provider"},
	{ID: "project", Label: "/project", Hint: "switch project", Category: "Switch", Action: "switch:project"},
	{ID: "processes", Label: "/processes", Hint: "running · kill", Category: "Panels", Action: "panel:process"},
	{ID: "workspace", Label: "/workspace", Hint: "connectors", Category: "Panels", Action: "panel:workspace"},
	{ID: "source", Label: "/source", Hint: "git changes", Category: "Panels", Action: "panel:source"},
	{ID: "context", Label: "/context", Hint: "files", Category: "Panels", Action: "panel:context"},
	{ID: "commands", Label: "/commands", Hint: "gate log", Category: "Views", Action: "view:commands"},
	{ID: "approvals", Label: "/approvals", Hint: "pending", Category: "Views", Action: "view:approvals"},
	{ID: "raw", Label: "/raw", Hint: "transcript", Category: "Views", Action: "view:raw"},
}

// apiComposerCommands handles GET /api/composer/commands — the `/` command menu:
// built-in actions followed by installed skills (insert-type). This is the
// single source the composer reads, so new commands/skills appear without an FE
// rebuild (as long as any new Action id has an FE handler).
//
// ?scope=new (the new-session page, before a session exists) drops the built-in
// actions — open-panel / change-view / switch-provider only make sense against a
// live session, and provider/project already have toolbar dropdowns there — so
// only skills (insert-type) are returned. The default scope returns everything.
func apiComposerCommands(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	out := make([]ComposerCommand, 0, len(builtinComposerCommands)+8)
	if c.Query("scope") != "new" {
		out = append(out, builtinComposerCommands...)
	}

	// ?provider=<type> (claude/codex/gemini) scopes skills to that provider —
	// each provider has its own skills dir, so their `/` menus differ. Empty
	// returns every skill.
	providerType := c.Query("provider")

	// A skill is a FOLDER holding a SKILL.md (loose files like CHANGELOG.md /
	// install_skills.sh that happen to sit in the skills dir are not skills).
	// The invokable name + description come from the SKILL.md frontmatter.
	for _, s := range cachedSkills() {
		if !s.IsDir {
			continue
		}
		if providerType != "" && !skillInProvider(s, providerType) {
			continue
		}
		name := s.Meta["name"]
		if name == "" {
			name = s.Name // fall back to the folder name if no frontmatter name
		}
		hint := truncate(s.Meta["description"], 60)
		if hint == "" {
			hint = "skill"
		}
		out = append(out, ComposerCommand{
			ID:       "skill:" + s.Name,
			Label:    "/" + name,
			Hint:     hint,
			Category: "Skills",
			Insert:   name,
		})
	}
	c.JSON(http.StatusOK, ComposerCommandsResponse{Commands: out})
}

// skillInProvider reports whether a skill exists in the given provider's dir
// (DirLabel yields the provider type, e.g. ".claude/skills" → "claude").
func skillInProvider(s skillsync.SkillInfo, providerType string) bool {
	for _, p := range s.InProviders {
		if p.Label == providerType {
			return true
		}
	}
	return false
}

// truncate shortens s to at most n runes, appending "…" when cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// skillsCache serves the skill scan stale-while-revalidate. Requests get the
// cached slice instantly; once it's older than the TTL, a SINGLE background
// goroutine re-scans (the `refreshing` guard prevents pile-ups that could hang
// under bursty requests). skillsync.ListSkills() walks every provider dir and
// reads each SKILL.md frontmatter, so caching keeps the composer `/` menu snappy
// while staying auto-up-to-date.
var skillsCache struct {
	mu         sync.Mutex
	skills     []skillsync.SkillInfo
	builtAt    time.Time
	loaded     bool
	refreshing bool
}

const skillsCacheTTL = 30 * time.Second

func cachedSkills() []skillsync.SkillInfo {
	skillsCache.mu.Lock()
	if !skillsCache.loaded {
		// First use: scan synchronously (off-lock) so callers get real data.
		skillsCache.mu.Unlock()
		s := skillsync.ListSkills()
		skillsCache.mu.Lock()
		skillsCache.skills = s
		skillsCache.builtAt = time.Now()
		skillsCache.loaded = true
		skillsCache.mu.Unlock()
		return s
	}
	s := skillsCache.skills
	if time.Since(skillsCache.builtAt) >= skillsCacheTTL && !skillsCache.refreshing {
		skillsCache.refreshing = true
		go func() {
			fresh := skillsync.ListSkills()
			skillsCache.mu.Lock()
			skillsCache.skills = fresh
			skillsCache.builtAt = time.Now()
			skillsCache.refreshing = false
			skillsCache.mu.Unlock()
		}()
	}
	skillsCache.mu.Unlock()
	return s
}
