package agents

import (
	"net/http"

	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/pkg/tool"
)

/* ── DTOs ────────────────────────────────────────────────────────────────── */

// PresetListItem is one row in GET /api/presets.
type PresetListItem struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default,omitempty"`
}

// PresetListResponse is the envelope for GET /api/presets.
type PresetListResponse struct {
	Presets []PresetListItem `json:"presets"`
}

// PresetDetailResponse is the envelope for GET /api/presets/{name}.
type PresetDetailResponse struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

/* ── handlers ────────────────────────────────────────────────────────────── */

// apiPresetList handles GET /api/presets and returns all preset names.
func apiPresetList(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	names := globalMgr.Registry().PresetNames()
	items := make([]PresetListItem, 0, len(names))
	for _, n := range names {
		items = append(items, PresetListItem{
			Name:      n,
			IsDefault: n == preset.DefaultName,
		})
	}
	c.JSON(http.StatusOK, PresetListResponse{Presets: items})
}

// apiPresetDetail handles GET /api/presets/{name} and returns the preset body.
func apiPresetDetail(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	name := c.PathValue("name")
	p, err := preset.Load(globalLayout, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "preset not found"})
		return
	}
	c.JSON(http.StatusOK, PresetDetailResponse{
		Name: p.Name,
		Body: p.Body,
	})
}
