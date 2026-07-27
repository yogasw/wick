package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	wick "github.com/yogasw/wick/internal/agents/provider/wick"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

// providers_wick.go holds the built-in wick provider's model CRUD,
// instance-settings, and live model-discovery handlers. The wick
// instance is single (wick/wick); these endpoints mutate its WickModels
// / WickConfig rather than creating instances.

const wickInstanceName = "wick"

// getWickInteractions returns the per-interaction records logged by the
// in-process wick engine for a session (<SessionDir>/wick-interactions.jsonl):
// the request (system + messages + tools) and response (text + tool calls
// + tokens + latency + error) for every model call. This is the wick
// provider's session log — "why did the model answer this" — surfaced by
// the FE in place of the CLI providers' Reproduce/argv box. Records carry
// no secrets (the API key lives in the transport header, never here).
// GET /providers/wick/interactions/{session}
func getWickInteractions(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	session := strings.TrimSpace(c.PathValue("session"))
	if session == "" || strings.ContainsAny(session, `/\`) || strings.Contains(session, "..") {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid session id"})
		return
	}
	// Server-side search + sort + pagination so a session with hundreds of
	// calls doesn't ship the whole log to the browser. q filters on the
	// text fields; sort=oldest|newest (default newest); page (1-based) +
	// page_size bound the slice.
	q := strings.ToLower(strings.TrimSpace(c.Query("q")))
	sortOldest := c.Query("sort") == "oldest"
	pageSize := clampInt(atoiOr(c.Query("page_size"), 10), 1, 100)
	page := maxInt(atoiOr(c.Query("page"), 1), 1)

	path := filepath.Join(globalLayout.SessionDir(session), "wick-interactions.jsonl")
	type row struct {
		raw json.RawMessage
		seq int
	}
	var rows []row
	_ = storage.ReadJSONL(path, func(line []byte) bool {
		if q != "" && !bytesContainsFold(line, q) {
			return true // filter before keeping — cheap substring on the raw line
		}
		var probe struct {
			Seq int `json:"seq"`
		}
		_ = json.Unmarshal(line, &probe)
		cp := make([]byte, len(line))
		copy(cp, line)
		rows = append(rows, row{raw: json.RawMessage(cp), seq: probe.Seq})
		return true
	})

	// File is append-order (oldest→newest). Reverse for the default newest-first.
	if !sortOldest {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}

	total := len(rows)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	out := make([]json.RawMessage, 0, end-start)
	for _, r := range rows[start:end] {
		out = append(out, r.raw)
	}
	resp := map[string]any{
		"interactions": out,
		"total":        total,
		"page":         page,
		"page_size":    pageSize,
	}
	// Live phase: is an outbound model call in flight right now? The log can't
	// show this (records land only when a call finishes), so the FE uses it to
	// label the live row as "model call" vs "running tool" accurately, how long
	// it's been waiting (to spot a stuck call), the retry attempt/reason, and the
	// in-flight request itself (so it can be viewed/copied-as-curl BEFORE the
	// record lands).
	if st := wick.ModelCallSnapshot(session); st.InFlight {
		live := map[string]any{
			"in_flight":  true,
			"elapsed_ms": st.Elapsed.Milliseconds(),
			"attempt":    st.Attempt,
			"kind":       st.Kind,
			"model":      st.Model,
			"model_id":   st.ModelID,
			"system":     st.System,
			"tools":      st.Tools,
			"messages":   st.Messages,
		}
		if st.RetryReason != "" {
			live["retry_reason"] = st.RetryReason
		}
		resp["model_call"] = live
		// Back-compat with the existing FE fields.
		resp["model_call_in_flight"] = true
		resp["model_call_elapsed_ms"] = st.Elapsed.Milliseconds()
	}
	c.JSON(http.StatusOK, resp)
}

// bytesContainsFold reports whether the lowercased line contains needle
// (needle is already lowercase). Cheap raw-line substring match — good
// enough for the interaction search; the JSONL line already holds every
// searchable field (model, response, tool_calls, request text).
func bytesContainsFold(line []byte, needle string) bool {
	return strings.Contains(strings.ToLower(string(line)), needle)
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// getWickInteractionCurl reconstructs a "copy as curl" command for one
// logged interaction, so an operator can paste-and-run the exact request
// wick sent to check whether the vendor API actually responded. The
// model's config (base_url, kind, api_format) is looked up on demand from
// the CURRENT WickModel by ID — never stored redundantly in the log — and
// the API key is always a placeholder, never resolved to plaintext here.
// GET /providers/wick/interactions/{session}/{seq}/curl
func getWickInteractionCurl(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	session := strings.TrimSpace(c.PathValue("session"))
	if session == "" || strings.ContainsAny(session, `/\`) || strings.Contains(session, "..") {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid session id"})
		return
	}
	seq, err := strconv.Atoi(c.PathValue("seq"))
	if err != nil || seq <= 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid seq"})
		return
	}

	path := filepath.Join(globalLayout.SessionDir(session), "wick-interactions.jsonl")
	var target json.RawMessage
	_ = storage.ReadJSONL(path, func(line []byte) bool {
		var probe struct {
			Seq int `json:"seq"`
		}
		if json.Unmarshal(line, &probe) == nil && probe.Seq == seq {
			cp := make([]byte, len(line))
			copy(cp, line)
			target = json.RawMessage(cp)
			return false // found it, stop scanning
		}
		return true
	})
	if target == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "interaction not found"})
		return
	}

	var probe struct {
		ModelID string `json:"model_id"`
	}
	_ = json.Unmarshal(target, &probe)
	if probe.ModelID == "" {
		c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "this interaction predates model-id tracking — cannot reconstruct a curl"})
		return
	}

	ins, err := wickInstance()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var model *provider.WickModel
	for i := range ins.WickModels {
		if ins.WickModels[i].ID == probe.ModelID {
			model = &ins.WickModels[i]
			break
		}
	}
	if model == nil {
		c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "the model this interaction ran under no longer exists"})
		return
	}

	req, err := wick.BuildRequest(target, *model)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	// Return every render up-front (key as a placeholder) so the FE builder
	// can switch format without a refetch. The real key is fetched only on
	// explicit reveal via the separate models/{id}/key endpoint — it never
	// rides in this payload.
	const ph = "$WICK_MODEL_API_KEY"
	c.JSON(http.StatusOK, map[string]any{
		// "curl" kept for backward-compat with the old single-command client.
		"curl":        wick.RenderSingleLine(req, ph),
		"single_line": wick.RenderSingleLine(req, ph),
		"multiline":   wick.RenderMultiline(req, ph),
		"raw_http":    wick.RenderRawHTTP(req, ph),
		"json_body":   wick.RenderJSONBody(req),
		"model_id":    model.ID,
		"env":         req.EnvHint,
	})
}

// findWickModel resolves a WickModel by id from the current instance. Returns
// (nil, false) when the instance can't load or the id is unknown.
func findWickModel(id string) (*provider.WickModel, bool) {
	ins, err := wickInstance()
	if err != nil {
		return nil, false
	}
	for i := range ins.WickModels {
		if ins.WickModels[i].ID == id {
			return &ins.WickModels[i], true
		}
	}
	return nil, false
}

// getWickLiveCurl reconstructs the curl/request for the CURRENTLY in-flight
// model call (no seq — reads the live-phase snapshot). Lets an operator inspect
// or replay the request that's being sent right now, e.g. while a call is stuck
// at "model call in progress (1m 17s)". Admin-only; key is a placeholder.
// GET /providers/wick/interactions/{session}/live/curl
func getWickLiveCurl(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	session := strings.TrimSpace(c.PathValue("session"))
	if session == "" || strings.ContainsAny(session, `/\`) || strings.Contains(session, "..") {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid session id"})
		return
	}
	st := wick.ModelCallSnapshot(session)
	if !st.InFlight {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no model call in flight"})
		return
	}
	if st.ModelID == "" {
		c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "in-flight call has no model id — cannot reconstruct a curl"})
		return
	}
	model, ok := findWickModel(st.ModelID)
	if !ok {
		c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "the in-flight model is not resolvable"})
		return
	}
	req, err := wick.BuildRequestFromLive(st, *model)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	const ph = "$WICK_MODEL_API_KEY"
	c.JSON(http.StatusOK, map[string]any{
		"single_line": wick.RenderSingleLine(req, ph),
		"multiline":   wick.RenderMultiline(req, ph),
		"raw_http":    wick.RenderRawHTTP(req, ph),
		"json_body":   wick.RenderJSONBody(req),
		"model_id":    model.ID,
		"env":         req.EnvHint,
	})
}

// cancelWickModelCall aborts just the in-flight model call for a session (not
// the turn/session). The engine sees the cancellation as a call error and ends
// the turn gracefully; the process stays alive. Admin-only.
// POST /providers/wick/interactions/{session}/cancel-call
func cancelWickModelCall(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	session := strings.TrimSpace(c.PathValue("session"))
	if session == "" || strings.ContainsAny(session, `/\`) || strings.Contains(session, "..") {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid session id"})
		return
	}
	if wick.CancelModelCall(session) {
		c.JSON(http.StatusOK, map[string]any{"cancelled": true})
		return
	}
	c.JSON(http.StatusConflict, map[string]any{"cancelled": false, "error": "no model call in flight"})
}

// getWickModelKey reveals a model's API key in plaintext for the curl
// builder's "use real token" option. Admin-only, and deliberately scoped:
// the admin is the one who entered this key, so returning it to them (never
// to the model, never to the log) is not a new exposure — it just saves a
// round-trip to the provider settings. Decrypts on demand via the configs
// service; a plaintext-stored key passes through unchanged.
// GET /providers/wick/models/{id}/key
func getWickModelKey(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	id := strings.TrimSpace(c.PathValue("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid model id"})
		return
	}
	ins, err := wickInstance()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var model *provider.WickModel
	for i := range ins.WickModels {
		if ins.WickModels[i].ID == id {
			model = &ins.WickModels[i]
			break
		}
	}
	if model == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "model not found"})
		return
	}
	key := model.APIKey
	if globalConfigs != nil && key != "" {
		if plain, derr := globalConfigs.DecryptSecret(key); derr == nil {
			key = plain
		}
	}
	c.JSON(http.StatusOK, map[string]string{"key": key})
}

// wickModelDTO is the create/edit payload from the Add-Model modal.
type wickModelDTO struct {
	ID              string   `json:"id,omitempty"`
	Kind            string   `json:"kind"`
	Label           string   `json:"label,omitempty"`
	Model           string   `json:"model"`
	APIKey          string   `json:"api_key,omitempty"`
	BaseURL         string   `json:"base_url,omitempty"`
	APIFormat       string   `json:"api_format,omitempty"`
	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
	Default         bool     `json:"default,omitempty"`
	Disabled        bool     `json:"disabled,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"top_p,omitempty"`
	ThinkingBudget  *int     `json:"thinking_budget,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	RawConfig       string   `json:"raw_config,omitempty"`
	// LiveSet marks this a live model set (Model may be empty; filter is
	// optional). See userconfig.WickModel.LiveSet.
	LiveSet bool `json:"live_set,omitempty"`
	// DiscoveryFilter narrows a live set's fetched list; empty = match all.
	// Only meaningful when LiveSet. See userconfig.WickModel.DiscoveryFilter.
	DiscoveryFilter string `json:"discovery_filter,omitempty"`
	// DefaultVendorModel pins the sticky default within a live set. Only
	// honored when LiveSet. Sent by the Add/Edit live-set form so the pin is
	// saved atomically with the entry. Empty on a live set clears the pin
	// (→ top-of-list); on a plain model it's ignored.
	DefaultVendorModel string `json:"default_vendor_model,omitempty"`
}

// wickInstance loads the single wick instance, materialising the
// default (empty) one when the user hasn't saved anything yet.
func wickInstance() (provider.Instance, error) {
	return provider.Find(provider.TypeWick, wickInstanceName)
}

// wickCapabilityPrefs returns the capability-chip display prefs from the wick
// instance's config: (show, mode). Defaults to (true, "icon") — chips on,
// icon mode — when nothing is configured. HideCapabilities is stored inverted,
// so show = !HideCapabilities.
func wickCapabilityPrefs() (show bool, mode string) {
	show, mode = true, "list"
	ins, err := wickInstance()
	if err != nil || ins.WickConfig == nil {
		return
	}
	show = !ins.WickConfig.HideCapabilities
	if m := strings.TrimSpace(ins.WickConfig.CapabilityDisplayMode); m != "" {
		mode = m
	}
	return
}

// wickModelView is one model as returned to the UI — key masked, never
// the ciphertext or plaintext.
type wickModelView struct {
	ID                 string   `json:"id"`
	Kind               string   `json:"kind"`
	Label              string   `json:"label"`
	Model              string   `json:"model"`
	KeyMasked          string   `json:"key_masked"`
	HasKey             bool     `json:"has_key"`
	BaseURL            string   `json:"base_url"`
	APIFormat          string   `json:"api_format"`
	MaxOutputTokens    int      `json:"max_output_tokens"`
	Default            bool     `json:"default"`
	Disabled           bool     `json:"disabled,omitempty"`
	Temperature        *float64 `json:"temperature,omitempty"`
	TopP               *float64 `json:"top_p,omitempty"`
	ThinkingBudget     *int     `json:"thinking_budget,omitempty"`
	ReasoningEffort    string   `json:"reasoning_effort,omitempty"`
	RawConfig          string   `json:"raw_config"`
	LiveSet            bool     `json:"live_set,omitempty"`
	DiscoveryFilter    string   `json:"discovery_filter,omitempty"`
	DefaultVendorModel string   `json:"default_vendor_model,omitempty"`
}

// getWickConfig returns the wick instance's models (masked keys) +
// settings for the detail page.
// GET /providers/wick/config
func getWickConfig(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	ins, err := wickInstance()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	models := make([]wickModelView, 0, len(ins.WickModels))
	for _, m := range ins.WickModels {
		v := wickModelView{
			ID:                 m.ID,
			Kind:               m.Kind,
			Label:              m.Label,
			Model:              m.Model,
			HasKey:             m.APIKey != "",
			KeyMasked:          maskKey(m.APIKey),
			BaseURL:            m.BaseURL,
			APIFormat:          m.APIFormat,
			MaxOutputTokens:    m.MaxOutputTokens,
			Default:            m.Default,
			Disabled:           m.Disabled,
			RawConfig:          m.RawConfig,
			LiveSet:            m.LiveSet,
			DiscoveryFilter:    m.DiscoveryFilter,
			DefaultVendorModel: m.DefaultVendorModel,
		}
		if m.GenConfig != nil {
			v.Temperature = m.GenConfig.Temperature
			v.TopP = m.GenConfig.TopP
			v.ThinkingBudget = m.GenConfig.ThinkingBudget
			v.ReasoningEffort = m.GenConfig.ReasoningEffort
		}
		models = append(models, v)
	}
	settings := map[string]any{
		"shell_tool_disabled":     false,
		"hide_capabilities":       false,
		"capability_display_mode": "",
		"connectors":              []string{},
		"max_context_tokens":      0,
		"max_turns":               0,
		"max_consec_errors":       0,
		"max_turn_minutes":        0,
		"max_model_retries":       0,
		"model_call_timeout_sec":  0,
		"reasoning_effort":        "",
		"raw_config":              "",
	}
	if wc := ins.WickConfig; wc != nil {
		settings["shell_tool_disabled"] = wc.ShellToolDisabled
		settings["hide_capabilities"] = wc.HideCapabilities
		settings["capability_display_mode"] = wc.CapabilityDisplayMode
		settings["connectors"] = wc.Connectors
		settings["max_context_tokens"] = wc.MaxContextTokens
		settings["max_turns"] = wc.MaxTurns
		settings["max_consec_errors"] = wc.MaxConsecErrors
		settings["max_turn_minutes"] = wc.MaxTurnMinutes
		settings["max_model_retries"] = wc.MaxModelRetries
		settings["model_call_timeout_sec"] = wc.ModelCallTimeoutSec
		settings["raw_config"] = wc.RawConfig
		if wc.GenConfig != nil {
			settings["temperature"] = wc.GenConfig.Temperature
			settings["top_p"] = wc.GenConfig.TopP
			settings["thinking_budget"] = wc.GenConfig.ThinkingBudget
			settings["reasoning_effort"] = wc.GenConfig.ReasoningEffort
		}
	}
	c.JSON(http.StatusOK, map[string]any{"models": models, "settings": settings})
}

// maskKey renders a stored (encrypted) key as a short masked marker for
// the UI — never the ciphertext. Empty stays empty.
func maskKey(stored string) string {
	if stored == "" {
		return ""
	}
	return "••••••••"
}

// saveWickModel creates or updates one model on the wick instance.
// POST /providers/wick/models
func saveWickModel(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	var dto wickModelDTO
	if err := c.BindJSON(&dto); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	dto.Model = strings.TrimSpace(dto.Model)
	dto.Kind = strings.ToLower(strings.TrimSpace(dto.Kind))
	dto.DiscoveryFilter = strings.TrimSpace(dto.DiscoveryFilter)
	// A live model set (LiveSet=true) needs no single model id and its filter
	// is OPTIONAL (empty = match all the vendor's models); a plain entry still
	// requires a model id.
	if dto.Kind == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "kind is required"})
		return
	}
	if !dto.LiveSet && dto.Model == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "model is required (or mark it a live set)"})
		return
	}
	if dto.Kind == "other" && strings.TrimSpace(dto.BaseURL) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "base_url is required for the 'other' provider kind"})
		return
	}

	ins, err := wickInstance()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ins.Type = provider.TypeWick
	ins.Name = wickInstanceName

	m := provider.WickModel{
		ID:              dto.ID,
		Kind:            dto.Kind,
		Label:           strings.TrimSpace(dto.Label),
		Model:           dto.Model,
		BaseURL:         strings.TrimSpace(dto.BaseURL),
		APIFormat:       strings.TrimSpace(dto.APIFormat),
		MaxOutputTokens: dto.MaxOutputTokens,
		Default:         dto.Default && !dto.Disabled,
		Disabled:        dto.Disabled,
		RawConfig:       strings.TrimSpace(dto.RawConfig),
		LiveSet:         dto.LiveSet,
		DiscoveryFilter: dto.DiscoveryFilter,
	}
	if g := genConfigFromDTO(dto); g != nil {
		m.GenConfig = g
	}

	// Find existing by ID to preserve its stored key when the form sends
	// a masked placeholder (unchanged key), and its live-set default-vendor
	// pin (unless the form explicitly sends one — the Add/Edit live-set form
	// carries default_vendor_model so the pin saves atomically with the entry;
	// the dedicated endpoint is the other way to change it).
	existingKey := ""
	idx := -1
	for i, em := range ins.WickModels {
		if dto.ID != "" && em.ID == dto.ID {
			idx = i
			existingKey = em.APIKey
			m.DefaultVendorModel = em.DefaultVendorModel
			break
		}
	}
	// A live-set form always authoritatively sets the pin (empty = clear);
	// a plain model never carries one. This runs after the preserve above so
	// an explicit form value wins over the stored one.
	if m.LiveSet {
		m.DefaultVendorModel = strings.TrimSpace(dto.DefaultVendorModel)
	} else {
		m.DiscoveryFilter = ""
		m.DefaultVendorModel = ""
	}
	if m.ID == "" {
		m.ID = "m_" + uuid.NewString()[:12]
	}
	// Encrypt a freshly-entered key; keep the stored one when the field is
	// empty or a masked placeholder.
	raw := strings.TrimSpace(dto.APIKey)
	if raw == "" || strings.ContainsRune(raw, '•') {
		m.APIKey = existingKey
	} else {
		m.APIKey = encryptSecretValue(raw)
	}

	if idx >= 0 {
		ins.WickModels[idx] = m
	} else {
		ins.WickModels = append(ins.WickModels, m)
	}
	normalizeDefault(&ins, m.ID, dto.Default)

	if err := provider.Save(ins); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "ok", "id": m.ID})
}

// deleteWickModel removes one model.
// DELETE /providers/wick/models/{id}
func deleteWickModel(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	id := c.PathValue("id")
	ins, err := wickInstance()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ins.Type = provider.TypeWick
	ins.Name = wickInstanceName
	out := ins.WickModels[:0]
	removedDefault := false
	for _, m := range ins.WickModels {
		if m.ID == id {
			removedDefault = m.Default
			continue
		}
		out = append(out, m)
	}
	ins.WickModels = out
	if removedDefault {
		for i := range ins.WickModels {
			if !ins.WickModels[i].Disabled {
				ins.WickModels[i].Default = true
				break
			}
		}
	}
	if err := provider.Save(ins); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

// setWickDefaultModel marks one model as the instance default.
// POST /providers/wick/models/{id}/default
func setWickDefaultModel(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	id := c.PathValue("id")
	ins, err := wickInstance()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ins.Type = provider.TypeWick
	ins.Name = wickInstanceName
	target := -1
	for i := range ins.WickModels {
		if ins.WickModels[i].ID == id {
			target = i
			break
		}
	}
	if target == -1 {
		c.JSON(http.StatusNotFound, map[string]string{"error": "model not found"})
		return
	}
	if ins.WickModels[target].Disabled {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "model is disabled — enable it first"})
		return
	}
	for i := range ins.WickModels {
		ins.WickModels[i].Default = i == target
	}
	if err := provider.Save(ins); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

type setDefaultVendorReq struct {
	VendorModel string `json:"vendor_model"`
}

// setWickDefaultVendorModel pins the sticky default vendor model WITHIN a live
// set (the model chosen when the set is picked without an explicit @vendor
// override). Empty vendor_model clears the pin (→ top-of-list default). The
// target entry must be a live set (DiscoveryFilter set); a plain model has no
// vendor sub-list. Stickiness + auto-fallback-when-missing is handled at
// picker time by expandLiveWickSet.
// POST /providers/wick/models/{id}/default-vendor
func setWickDefaultVendorModel(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	id := c.PathValue("id")
	var req setDefaultVendorReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	ins, err := wickInstance()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ins.Type = provider.TypeWick
	ins.Name = wickInstanceName
	target := -1
	for i := range ins.WickModels {
		if ins.WickModels[i].ID == id {
			target = i
			break
		}
	}
	if target == -1 {
		c.JSON(http.StatusNotFound, map[string]string{"error": "model not found"})
		return
	}
	if !ins.WickModels[target].LiveSet {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "not a live model set — a plain model has no vendor sub-list"})
		return
	}
	ins.WickModels[target].DefaultVendorModel = strings.TrimSpace(req.VendorModel)
	if err := provider.Save(ins); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

type setDisabledReq struct {
	Disabled bool `json:"disabled"`
}

// setWickModelDisabled toggles a model's Disabled flag. Disabling the
// current default model promotes the first other enabled model to
// default (mirrors deleteWickModel's fallback); disabling the only
// enabled model leaves no default — the composer/spawn path already
// treats "no enabled default" as "no model available".
// POST /providers/wick/models/{id}/disabled
func setWickModelDisabled(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	id := c.PathValue("id")
	var req setDisabledReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	ins, err := wickInstance()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ins.Type = provider.TypeWick
	ins.Name = wickInstanceName
	target := -1
	for i := range ins.WickModels {
		if ins.WickModels[i].ID == id {
			target = i
			break
		}
	}
	if target == -1 {
		c.JSON(http.StatusNotFound, map[string]string{"error": "model not found"})
		return
	}
	ins.WickModels[target].Disabled = req.Disabled
	if req.Disabled && ins.WickModels[target].Default {
		ins.WickModels[target].Default = false
		for i := range ins.WickModels {
			if i != target && !ins.WickModels[i].Disabled {
				ins.WickModels[i].Default = true
				break
			}
		}
	}
	if err := provider.Save(ins); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// duplicateWickModel clones a model's full config (including its
// encrypted key, copied verbatim — no decrypt/re-encrypt needed since the
// ciphertext is valid for any row) under a new id, labeled "(copy)". The
// clone is never Default or Disabled regardless of the source, so a
// duplicate for experimentation doesn't silently take over as the
// instance's active model.
// POST /providers/wick/models/{id}/duplicate
func duplicateWickModel(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	id := c.PathValue("id")
	ins, err := wickInstance()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ins.Type = provider.TypeWick
	ins.Name = wickInstanceName
	var src *provider.WickModel
	for i := range ins.WickModels {
		if ins.WickModels[i].ID == id {
			src = &ins.WickModels[i]
			break
		}
	}
	if src == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "model not found"})
		return
	}
	clone := *src
	clone.ID = "m_" + uuid.NewString()[:12]
	clone.Label = strings.TrimSpace(src.Label)
	if clone.Label == "" {
		clone.Label = src.Model
	}
	clone.Label += " (copy)"
	clone.Default = false
	clone.Disabled = false
	if src.GenConfig != nil {
		g := *src.GenConfig
		clone.GenConfig = &g
	}
	ins.WickModels = append(ins.WickModels, clone)
	if err := provider.Save(ins); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok", "id": clone.ID})
}

// testWickModel sends a real 1-token ping through the model's own adapter
// (same resolveModel path a spawn uses) to validate the key + model id +
// base URL in one round-trip.
// POST /providers/wick/models/{id}/test
func testWickModel(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	id := c.PathValue("id")
	ins, err := wickInstance()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var m *provider.WickModel
	for i := range ins.WickModels {
		if ins.WickModels[i].ID == id {
			m = &ins.WickModels[i]
			break
		}
	}
	if m == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "model not found"})
		return
	}
	testM := *m
	if globalConfigs != nil {
		plain, derr := globalConfigs.DecryptSecret(testM.APIKey)
		if derr != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "stored key could not be decrypted"})
			return
		}
		testM.APIKey = plain
	}
	if testM.APIKey == "" && strings.ToLower(testM.Kind) != "other" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "no API key stored for this model"})
		return
	}
	latency, err := wick.TestModel(c.Context(), testM)
	if err != nil {
		c.JSON(http.StatusOK, map[string]any{"ok": false, "error": wick.TestModelErrorMessage(err)})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "latency_ms": latency.Milliseconds()})
}

// wickSettingsDTO is the Provider-settings card payload.
type wickSettingsDTO struct {
	ShellToolDisabled   bool     `json:"shell_tool_disabled"`
	HideCapabilities    bool     `json:"hide_capabilities"`
	CapabilityDisplayMode string `json:"capability_display_mode,omitempty"`
	Connectors          []string `json:"connectors,omitempty"`
	MaxContextTokens    int      `json:"max_context_tokens,omitempty"`
	MaxTurns            int      `json:"max_turns,omitempty"`
	MaxConsecErrors     int      `json:"max_consec_errors,omitempty"`
	MaxTurnMinutes      int      `json:"max_turn_minutes,omitempty"`
	MaxModelRetries     int      `json:"max_model_retries,omitempty"`
	ModelCallTimeoutSec int      `json:"model_call_timeout_sec,omitempty"`
	Temperature         *float64 `json:"temperature,omitempty"`
	TopP                *float64 `json:"top_p,omitempty"`
	ThinkingBudget      *int     `json:"thinking_budget,omitempty"`
	ReasoningEffort     string   `json:"reasoning_effort,omitempty"`
	RawConfig           string   `json:"raw_config,omitempty"`
}

// saveWickSettings persists the instance-level WickConfig.
// POST /providers/wick/settings
func saveWickSettings(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	var dto wickSettingsDTO
	if err := c.BindJSON(&dto); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	ins, err := wickInstance()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ins.Type = provider.TypeWick
	ins.Name = wickInstanceName
	wc := &provider.WickConfig{
		ShellToolDisabled:     dto.ShellToolDisabled,
		HideCapabilities:      dto.HideCapabilities,
		CapabilityDisplayMode: strings.TrimSpace(dto.CapabilityDisplayMode),
		Connectors:            dto.Connectors,
		MaxContextTokens:      dto.MaxContextTokens,
		MaxTurns:              dto.MaxTurns,
		MaxConsecErrors:       dto.MaxConsecErrors,
		MaxTurnMinutes:        dto.MaxTurnMinutes,
		MaxModelRetries:       dto.MaxModelRetries,
		ModelCallTimeoutSec:   dto.ModelCallTimeoutSec,
		RawConfig:             strings.TrimSpace(dto.RawConfig),
	}
	if g := genConfigFromGen(dto.Temperature, dto.TopP, dto.ThinkingBudget, strings.TrimSpace(dto.ReasoningEffort), 0); g != nil {
		wc.GenConfig = g
	}
	ins.WickConfig = wc
	if err := provider.Save(ins); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// discoverWickModels proxies a vendor's model-list API so keys never
// reach the browser. Body: {kind, api_key?, base_url?, model_ref?}.
// model_ref reuses an already-stored (encrypted) key.
// POST /providers/wick/models/discover
func discoverWickModels(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	var body struct {
		Kind     string `json:"kind"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
		ModelRef string `json:"model_ref"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	key := strings.TrimSpace(body.APIKey)
	// A masked or empty key + a model_ref → reuse the stored key.
	if (key == "" || strings.ContainsRune(key, '•')) && body.ModelRef != "" {
		key = storedWickKey(body.ModelRef)
	}
	if key == "" && strings.ToLower(body.Kind) != "other" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "api key required to list models"})
		return
	}
	models, err := wick.DiscoverModels(c.Context(), body.Kind, key, strings.TrimSpace(body.BaseURL))
	if err != nil {
		// Non-fatal: the UI falls back to a free-text model id.
		log.Ctx(c.Context()).Warn().Err(err).Str("kind", body.Kind).Msg("wick model discovery failed")
		c.JSON(http.StatusOK, map[string]any{"models": []wick.DiscoveredModel{}, "error": err.Error()})
		return
	}
	if models == nil {
		models = []wick.DiscoveredModel{}
	}
	c.JSON(http.StatusOK, map[string]any{"models": models})
}

// expandLiveWickSet fetches the vendor model list for ONE registered wick
// model that carries a discovery filter, and returns the matching models. Used
// by the picker's 4th level: the user opened a "live set" row, so we resolve
// it to real vendor models now. Empty result / error → nil (caller shows an
// empty state); never aborts. Matching an entry by id, else by filter string.
func expandLiveWickSet(ctx context.Context, ins provider.Instance, entryID, filter string) []view.ModelChoiceVM {
	var target *provider.WickModel
	for i := range ins.WickModels {
		m := &ins.WickModels[i]
		if m.Disabled || !m.LiveSet {
			continue
		}
		if (entryID != "" && m.ID == entryID) || (entryID == "" && m.DiscoveryFilter == filter) {
			target = m
			break
		}
	}
	if target == nil {
		return nil
	}
	live, err := discoverWickModel(ctx, target)
	if err != nil {
		log.Ctx(ctx).Warn().Err(err).Str("filter", target.DiscoveryFilter).Msg("live wick model set fetch failed")
		return nil
	}
	filtered := wick.FilterModels(live, target.DiscoveryFilter)
	return markLiveSetDefault(filtered, target.DefaultVendorModel)
}

// markLiveSetDefault turns the filtered vendor list into picker choices with
// the sticky default marked: the pinned id wins if still present; otherwise
// the top of the list is the default (auto re-select when the pin vanished).
// Pure — the sticky/fallback rule is unit-tested here without network.
func markLiveSetDefault(filtered []wick.DiscoveredModel, pin string) []view.ModelChoiceVM {
	pin = strings.TrimSpace(pin)
	pinnedPresent := false
	for _, d := range filtered {
		if d.ID == pin {
			pinnedPresent = true
			break
		}
	}
	out := make([]view.ModelChoiceVM, 0, len(filtered))
	for i, d := range filtered {
		label := d.Label
		if label == "" {
			label = d.ID
		}
		isDefault := (pinnedPresent && d.ID == pin) || (!pinnedPresent && i == 0)
		out = append(out, view.ModelChoiceVM{ID: d.ID, Label: label, Default: isDefault, Caps: d.Caps})
	}
	return out
}

// liveSetDefaultVendor returns the effective default vendor model id for a
// live-set entry: the sticky pin if still present in the fresh list, else the
// top of the filtered list (auto re-select when the pin has vanished). Empty
// only when discovery fails / the list is empty.
func liveSetDefaultVendor(ctx context.Context, ins provider.Instance, entryID string) string {
	choices := expandLiveWickSet(ctx, ins, entryID, "")
	for _, c := range choices {
		if c.Default {
			return c.ID
		}
	}
	if len(choices) > 0 {
		return choices[0].ID
	}
	return ""
}

// discoverWickModel lists the vendor's models for one registered wick model,
// decrypting its stored key. DiscoverModels caches per key/kind/base with a
// TTL, so repeated drills are cheap.
func discoverWickModel(ctx context.Context, m *provider.WickModel) ([]wick.DiscoveredModel, error) {
	key := m.APIKey
	if globalConfigs != nil && key != "" {
		if plain, derr := globalConfigs.DecryptSecret(key); derr == nil {
			key = plain
		}
	}
	if key == "" && strings.ToLower(m.Kind) != "other" {
		return nil, fmt.Errorf("no stored key to list models")
	}
	return wick.DiscoverModels(ctx, m.Kind, key, strings.TrimSpace(m.BaseURL))
}

// storedWickKey returns the decrypted API key for a stored model id, or
// "" if not found / unavailable.
func storedWickKey(modelID string) string {
	ins, err := wickInstance()
	if err != nil {
		return ""
	}
	for _, m := range ins.WickModels {
		if m.ID == modelID {
			if globalConfigs == nil {
				return m.APIKey
			}
			plain, derr := globalConfigs.DecryptSecret(m.APIKey)
			if derr != nil {
				return ""
			}
			return plain
		}
	}
	return ""
}

// normalizeDefault enforces the single-default invariant. When the just-
// saved model is default, clear the others; if nothing is default, make
// the saved one default (unless it's disabled, in which case fall back to
// the first enabled model — a disabled model is never auto-picked).
func normalizeDefault(ins *provider.Instance, savedID string, savedDefault bool) {
	if savedDefault {
		for i := range ins.WickModels {
			ins.WickModels[i].Default = ins.WickModels[i].ID == savedID
		}
		return
	}
	for _, m := range ins.WickModels {
		if m.Default && !m.Disabled {
			return // some other enabled model is already default — fine
		}
	}
	// Nothing (enabled) is default → make the saved one default, unless
	// it's disabled itself, then fall back to the first enabled model.
	for i := range ins.WickModels {
		if ins.WickModels[i].ID == savedID && !ins.WickModels[i].Disabled {
			ins.WickModels[i].Default = true
			return
		}
	}
	for i := range ins.WickModels {
		if !ins.WickModels[i].Disabled {
			ins.WickModels[i].Default = true
			return
		}
	}
}

func genConfigFromDTO(dto wickModelDTO) *provider.WickGenConfig {
	return genConfigFromGen(dto.Temperature, dto.TopP, dto.ThinkingBudget, strings.TrimSpace(dto.ReasoningEffort), dto.MaxOutputTokens)
}

func genConfigFromGen(temp, topP *float64, thinking *int, reasoningEffort string, maxOut int) *provider.WickGenConfig {
	if temp == nil && topP == nil && thinking == nil && reasoningEffort == "" && maxOut == 0 {
		return nil
	}
	return &provider.WickGenConfig{
		Temperature:     temp,
		TopP:            topP,
		ThinkingBudget:  thinking,
		ReasoningEffort: reasoningEffort,
		MaxOutputTokens: maxOut,
	}
}
