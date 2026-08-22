package agents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	agentsconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/pkg/tool"
)

/* ── DTOs ────────────────────────────────────────────────────────────────── */

// projectWidgetReq is the widget CSP override as the editor submits it.
// The allowlist arrives as the raw textarea text rather than a []string so
// the operator's line breaks and typos survive round-tripping into the
// error message.
type projectWidgetReq struct {
	Override bool `json:"override"`
	// Mode is the preset — the one knob. The per-directive fields below are
	// stored either way (so a Custom setup survives a trip through Secure)
	// but are only READ when Mode is custom.
	Mode        string `json:"mode"`
	FrameSrc    string `json:"frame_src"`
	ImgSrc      string `json:"img_src"`
	MediaSrc    string `json:"media_src"`
	ConnectSrc  string `json:"connect_src"`
	ScriptSrc   string `json:"script_src"`
	AllowPopups bool   `json:"allow_popups"`
	// AllowPopupEscape drops the sandbox flags an opened tab inherits, so the
	// destination sees a real origin instead of "null". Meaningful only
	// alongside AllowPopups; the renderer treats it as implying that flag.
	AllowPopupEscape bool   `json:"allow_popup_escape"`
	Allowlist        string `json:"allowlist"`
}

// toPolicy validates the submitted override and converts it to the stored
// shape. Every host is normalised here, at the point of entry, so an
// operator who typed something that cannot work is told immediately rather
// than discovering later that a widget still cannot reach the host they
// thought they had allowed.
//
// A directive mode that is not one of block/list/all is rejected outright
// rather than coerced: coercing an unrecognised value would silently
// tighten or loosen the policy behind the operator's back.
func (r projectWidgetReq) toPolicy() (agentsconfig.WidgetPolicy, error) {
	out := agentsconfig.WidgetPolicy{
		Override:    r.Override,
		Mode:        r.Mode,
		FrameSrc:    r.FrameSrc,
		ImgSrc:      r.ImgSrc,
		MediaSrc:    r.MediaSrc,
		ConnectSrc:  r.ConnectSrc,
		ScriptSrc:   r.ScriptSrc,
		AllowPopups: r.AllowPopups,
		// Not stored without popups: an escape flag alone reads on the settings
		// screen as a permission that does something when no tab can open.
		AllowPopupEscape: r.AllowPopups && r.AllowPopupEscape,
	}
	switch r.Mode {
	case "", agentsconfig.PresetSecure, agentsconfig.PresetUnsecure, agentsconfig.PresetCustom:
	default:
		return agentsconfig.WidgetPolicy{}, fmt.Errorf("mode: unknown mode %q (want %s, %s, or %s)",
			r.Mode, agentsconfig.PresetSecure, agentsconfig.PresetUnsecure, agentsconfig.PresetCustom)
	}
	// The per-directive values are validated even under a preset that will
	// ignore them: they are still persisted, and storing a value that would
	// be rejected the moment the operator switches to Custom is worse than
	// refusing it now.
	for name, m := range map[string]string{
		"frame_src": r.FrameSrc, "img_src": r.ImgSrc, "media_src": r.MediaSrc,
		"connect_src": r.ConnectSrc, "script_src": r.ScriptSrc,
	} {
		switch m {
		case "", agentsconfig.ModeBlock, agentsconfig.ModeList, agentsconfig.ModeAll:
		default:
			return agentsconfig.WidgetPolicy{}, fmt.Errorf("%s: unknown mode %q (want block, list, or all)", name, m)
		}
	}
	hosts, err := agentsconfig.ValidateAllowlist(agentsconfig.ParseAllowlist(r.Allowlist))
	if err != nil {
		return agentsconfig.WidgetPolicy{}, err
	}
	out.Allowlist = hosts
	return out, nil
}

// ProjectSettingsResponse is the envelope for GET /api/projects/{id}.
type ProjectSettingsResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	CustomPath  string `json:"custom_path"`
	Managed     bool   `json:"managed"`
	// IsProtected reports whether the project cannot be deleted — the
	// built-in "default" project or an auto-created personal project.
	// The SPA hides the delete control when true.
	IsProtected     bool   `json:"is_protected"`
	IsNew           bool   `json:"is_new"`
	DefaultPreset   string `json:"default_preset"`
	DefaultProvider string `json:"default_provider"`
	// DefaultModel pins which model on DefaultProvider this project runs,
	// in that instance's own id space (for wick, down to a live-set leaf).
	// Only meaningful alongside DefaultProvider.
	DefaultModel string `json:"default_model"`
	SystemAddon  string `json:"system_addon"`
	// Widget is this project's stored HTML-artifact CSP override — the raw
	// override, NOT the resolved policy: the editor has to show what the
	// project itself sets, separately from what it inherits.
	Widget agentsconfig.WidgetPolicy `json:"widget"`
	// WidgetInherited is the global policy this project falls back to. The
	// editor shows it read-only so the operator can see what the project's
	// own hosts are being appended TO, instead of having to guess.
	WidgetInherited agentsconfig.WidgetPolicy `json:"widget_inherited"`
	// Ticket is this project's ticket-mode configuration, edited in the
	// settings SPA's "Ticket system" section and saved via the separate
	// PUT /api/projects/{id}/ticket-config endpoint.
	Ticket    project.TicketConfig `json:"ticket"`
	ChatCount int                  `json:"chat_count"`
	CreatedAt string               `json:"created_at"`
	PresetList      []string                  `json:"preset_list"`
	ProviderList    []ProviderListItem        `json:"provider_list"`
	Pinned          []ProjectPinnedSession    `json:"pinned"`
	MetaJSON        string                    `json:"meta_json"`
	Action          string                    `json:"action"`
}

// ProviderListItem is one selectable provider instance for the project
// defaults dropdown. The "type/name" pair is what Defaults.Provider
// stores; the SPA renders the value as "type/name" so a custom instance
// (e.g. claude/abc) is selectable, not just the base type.
type ProviderListItem struct {
	Type string `json:"type"`
	Name string `json:"name"`
	// Models are this instance's selectable models (id+label), so the
	// project default picker can descend to a model level — same data the
	// composer's provider picker uses. Empty for single-model instances.
	Models []ProviderModelItem `json:"models,omitempty"`
}

// ProviderModelItem is one model choice under a provider instance.
type ProviderModelItem struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Default bool   `json:"default"`
	Desc    string `json:"desc,omitempty"`
	// Live marks a live model SET, not a single model: the picker expands it
	// one level further (lazily, via the models endpoint with ?entry=) and
	// pins a chosen leaf as "<id>@<vendorModelID>". Without this flag the set
	// looked like an ordinary model whose id resolves to nothing runnable.
	Live bool `json:"live,omitempty"`
}

// ProjectPinnedSession is one pinned session row in the API response.
type ProjectPinnedSession struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// projectProviderList returns the selectable provider instances for the
// project defaults dropdown, sourced from the same cached status the
// new-session composer uses so both selectors agree on what's healthy.
func projectProviderList(c *tool.Ctx) []ProviderListItem {
	ps := providerChoicesCached(c.Context())
	out := make([]ProviderListItem, 0, len(ps))
	for _, p := range ps {
		var models []ProviderModelItem
		for _, m := range p.Models {
			models = append(models, ProviderModelItem{
				ID: m.ID, Label: m.Label, Default: m.Default, Desc: m.Desc, Live: m.Live,
			})
		}
		out = append(out, ProviderListItem{Type: p.Type, Name: p.Name, Models: models})
	}
	return out
}

/* ── handlers ────────────────────────────────────────────────────────────── */

// apiProjectDetail handles GET /api/projects/{id} and returns the full
// project settings payload consumed by the project-settings SPA.
func apiProjectDetail(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	presetList := globalMgr.Registry().PresetNames()
	providerList := projectProviderList(c)

	if id == "new" {
		c.JSON(http.StatusOK, ProjectSettingsResponse{
			IsNew:           true,
			Icon:            "📁",
			DefaultPreset:   "default",
			DefaultProvider: "",
			Managed:         true,
			PresetList:      presetList,
			ProviderList:    providerList,
			Action:          c.Base() + "/projects",
			Pinned:          []ProjectPinnedSession{},
		})
		return
	}

	// Access enforced by projectAccessMW (r.Use "/api/projects/{id}").
	p, ok := globalMgr.Registry().Project(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}

	resp := ProjectSettingsResponse{
		ID:              id,
		Name:            p.Meta.Name,
		Icon:            p.Meta.Icon,
		Description:     p.Meta.Description,
		CustomPath:      p.Meta.CustomPath,
		Managed:         p.Meta.CustomPath == "",
		IsProtected:     project.IsProtected(p.Meta),
		DefaultPreset:   p.Meta.Defaults.Preset,
		DefaultProvider: p.Meta.Defaults.Provider,
		DefaultModel:    p.Meta.Defaults.Model,
		SystemAddon:     p.Meta.Defaults.SystemAddon,
		CreatedAt:       p.Meta.CreatedAt.Format("2006-01-02"),
		PresetList:      presetList,
		ProviderList:    providerList,
		Action:          c.Base() + "/projects/" + id,
		Pinned:          []ProjectPinnedSession{},
		Widget:          p.Meta.Widget,
		WidgetInherited: globalWidgetPolicy(),
		Ticket:          p.Meta.Ticket,
	}

	// Chats, not sessions: a sub-agent runs in its leader's project but is
	// not a chat anyone started, and counting it inflates every project a
	// delegation has ever run in.
	for sid, s := range globalMgr.Registry().Sessions() {
		if s.Meta.ProjectID == id && s.Meta.ParentSessionID == "" {
			resp.ChatCount++
		}
		_ = sid
	}

	for _, pinID := range p.Meta.PinnedSessions {
		label := loadFirstUserMessage(globalLayout, pinID, 50)
		if label == "" {
			label = pinID
		}
		resp.Pinned = append(resp.Pinned, ProjectPinnedSession{ID: pinID, Label: label})
	}

	if b, err := json.MarshalIndent(p.Meta, "", "  "); err == nil {
		resp.MetaJSON = string(b)
	}

	c.JSON(http.StatusOK, resp)
}

// apiProjectUpdate handles POST /api/projects/{id} as JSON — SPA variant
// of updateProject. Accepts application/json body; returns {status:"ok"}.
func apiProjectUpdate(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")

	var req struct {
		Name        string `json:"name"`
		Icon        string `json:"icon"`
		Description string `json:"description"`
		FolderMode  string `json:"folder_mode"`
		CustomPath  string `json:"custom_path"`
		Preset      string `json:"preset"`
		Provider    string `json:"provider"`
		Model       string `json:"model"`
		SystemAddon string `json:"system_addon"`
		// Widget carries the project's CSP override. A nil pointer means
		// "leave it as it is" — distinct from a zero value, which means
		// "stop overriding and inherit the global policy". Older clients
		// that never send the field must not silently clear an override.
		Widget *projectWidgetReq `json:"widget"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	// Access enforced by projectAccessMW (r.Use "/api/projects/{id}").
	p, ok := globalMgr.Registry().Project(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}

	meta := p.Meta
	if v := strings.TrimSpace(req.Name); v != "" {
		meta.Name = v
	}
	if v := req.Icon; v != "" {
		meta.Icon = strings.TrimSpace(v)
	}
	meta.Description = req.Description
	if v := req.Preset; v != "" {
		meta.Defaults.Preset = v
	}
	meta.Defaults.Provider = strings.TrimSpace(req.Provider)
	meta.Defaults.Model = modelWithProvider(req.Provider, req.Model)
	meta.Defaults.SystemAddon = req.SystemAddon

	customPath := strings.TrimSpace(req.CustomPath)
	if req.FolderMode == "managed" {
		customPath = ""
	}
	meta.CustomPath = customPath

	// Rejected before anything is persisted: a half-applied security policy
	// is worse than a refused edit.
	if req.Widget != nil {
		w, err := req.Widget.toPolicy()
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		meta.Widget = w
	}

	if _, err := globalMgr.UpdateProject(c.Context(), id, meta); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
