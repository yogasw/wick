package handlers

import (
	"net/http"

	"github.com/yogasw/wick/internal/agents/skillsync"
	"github.com/yogasw/wick/internal/login"
)

// WickSkillList handles the wick_skill_list tool.
func WickSkillList(w http.ResponseWriter, req RPCRequest, rsp Responder) {
	skills := skillsync.ListSkills()
	// ReadDirs so the built-in dir shows up alongside the provider dirs; each
	// skill also carries a `builtin` flag for callers that must not offer to
	// edit or delete a shipped one.
	dirs := skillsync.ReadDirs()

	type providerDir struct {
		Label string `json:"label"`
		Dir   string `json:"dir"`
	}
	providers := make([]providerDir, 0, len(dirs))
	for _, d := range dirs {
		providers = append(providers, providerDir{Label: skillsync.DirLabel(d), Dir: d})
	}

	rsp.ToolJSON(w, req.ID, map[string]any{
		"providers": providers,
		"skills":    skills,
		"total":     len(skills),
	})
}

// WickSkillSync handles the wick_skill_sync tool.
func WickSkillSync(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder) {
	caller := login.GetUser(r.Context())
	if caller == nil || !caller.IsAdmin() {
		rsp.ToolError(w, req.ID, "forbidden: skill sync requires admin", "wick_skill_sync")
		return
	}

	res, err := skillsync.Sync()
	if err != nil {
		rsp.ToolError(w, req.ID, "skill sync: "+err.Error(), "wick_skill_sync")
		return
	}

	type providerDir struct {
		Label string `json:"label"`
		Dir   string `json:"dir"`
	}
	providers := make([]providerDir, 0, len(res.Dirs))
	for _, d := range res.Dirs {
		providers = append(providers, providerDir{Label: skillsync.DirLabel(d), Dir: d})
	}

	// skills_copied is the number that answers "did my skills move?" — copied
	// counts individual files, and the skill dirs also hold loose files
	// (README.md, CLAUDE.md) that inflate it without any skill syncing.
	rsp.ToolJSON(w, req.ID, map[string]any{
		"copied":        res.Copied,
		"skills_copied": res.SkillsCopied,
		"skipped":       res.Skipped,
		"errors":        res.Errors,
		"providers":     providers,
	})
}
