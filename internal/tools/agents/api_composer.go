package agents

import (
	"net/http"

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
func apiComposerCommands(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	out := make([]ComposerCommand, 0, len(builtinComposerCommands)+8)
	out = append(out, builtinComposerCommands...)

	files, _ := cachedSkillStatus()
	for _, f := range files {
		if f.IsDir {
			continue
		}
		out = append(out, ComposerCommand{
			ID:       "skill:" + f.Name,
			Label:    "/" + f.Name,
			Hint:     "skill",
			Category: "Skills",
			Insert:   f.Name,
		})
	}
	c.JSON(http.StatusOK, ComposerCommandsResponse{Commands: out})
}
