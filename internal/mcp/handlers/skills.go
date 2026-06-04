package handlers

import (
	"net/http"

	"github.com/yogasw/wick/internal/agents/skillsync"
)

// WickSkillList handles the wick_skill_list tool.
func WickSkillList(w http.ResponseWriter, req RPCRequest, rsp Responder) {
	skills := skillsync.ListSkills()
	dirs := skillsync.KnownDirs()

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
func WickSkillSync(w http.ResponseWriter, req RPCRequest, rsp Responder) {
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

	rsp.ToolJSON(w, req.ID, map[string]any{
		"copied":    res.Copied,
		"skipped":   res.Skipped,
		"errors":    res.Errors,
		"providers": providers,
	})
}
