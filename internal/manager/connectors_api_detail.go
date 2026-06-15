package manager

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// connectorRowJSON is the read model for one connector instance (row) the
// manager SPA renders on the per-connector list page. It mirrors the card
// built by connectorListCard: identity, status, and the access tag chips.
type connectorRowJSON struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Disabled     bool     `json:"disabled"`
	Status       string   `json:"status"`
	RateLimitRPM int      `json:"rate_limit_rpm"`
	Tags         []string `json:"tags"`
}

// connectorListJSON is the shape served at GET /manager/api/connectors/{key}:
// the connector-type metadata plus the caller's manageable rows.
type connectorListJSON struct {
	Key         string             `json:"key"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Icon        string             `json:"icon"`
	Fixed       bool               `json:"fixed"`
	OpCount     int                `json:"op_count"`
	Custom      bool               `json:"custom"`
	Rows        []connectorRowJSON `json:"rows"`
}

// configFieldJSON is the per-field config schema + value the SPA configs
// form renders. It is the JSON projection of entity.Config restricted to
// the presentation hints the widget renderer needs. Secret values are
// never disclosed — Value is blanked for secret fields, with HasValue
// signalling whether a stored secret exists (drives the "stored" badge).
type configFieldJSON struct {
	Key         string            `json:"key"`
	Type        string            `json:"type"`
	Value       string            `json:"value"`
	Options     string            `json:"options"`
	Required    bool              `json:"required"`
	IsSecret    bool              `json:"is_secret"`
	HasValue    bool              `json:"has_value"`
	Description string            `json:"description"`
	VisibleWhen string            `json:"visible_when"`
	ColOptions  map[string]string `json:"col_options,omitempty"`
	EnvOverride string            `json:"env_override"`
}

// connectorOpJSON is the per-operation read model for the detail page's
// operations table: identity, the destructive hint, and effective state.
type connectorOpJSON struct {
	Key                  string `json:"key"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	Destructive          bool   `json:"destructive"`
	Enabled              bool   `json:"enabled"`
	SystemDisabled       bool   `json:"system_disabled"`
	SystemDisabledReason string `json:"system_disabled_reason"`
}

// connectorDetailJSON is the shape served at
// GET /manager/api/connectors/{key}/{id}: the row identity, the connector
// type metadata, the visible config fields, and the operations table.
type connectorDetailJSON struct {
	Key            string            `json:"key"`
	Name           string            `json:"name"`
	Icon           string            `json:"icon"`
	ID             string            `json:"id"`
	Label          string            `json:"label"`
	Disabled       bool              `json:"disabled"`
	RateLimitRPM   int               `json:"rate_limit_rpm"`
	HasHealthCheck bool              `json:"has_health_check"`
	CanConfigure   bool              `json:"can_configure"`
	Fields         []configFieldJSON `json:"fields"`
	Operations     []connectorOpJSON `json:"operations"`
}

// apiConnectorRows serves GET /manager/api/connectors/{key}: the connector
// type metadata plus the caller's manageable instance rows. Visibility +
// access reuse visibleRowsForKey, identical to connectorListPage.
func (h *Handler) apiConnectorRows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")

	mod, ok := h.connectors.Module(key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown connector"})
		return
	}

	rows, err := h.visibleRowsForKey(r, user, key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	tagsByRow := h.resolveRowTags(ctx, rows)

	out := connectorListJSON{
		Key:         mod.Meta.Key,
		Name:        mod.Meta.Name,
		Description: mod.Meta.Description,
		Icon:        mod.Meta.Icon,
		Fixed:       mod.Meta.Fixed,
		OpCount:     len(mod.Operations),
		Custom:      h.customDefInfo(ctx, key, user) != nil,
		Rows:        make([]connectorRowJSON, 0, len(rows)),
	}
	for _, row := range rows {
		out.Rows = append(out.Rows, connectorRowJSON{
			ID:           row.ID,
			Label:        row.Label,
			Disabled:     row.Disabled,
			Status:       h.connectors.Status(row),
			RateLimitRPM: row.RateLimitRPM,
			Tags:         tagsByRow[row.ID],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// apiConnectorDetail serves GET /manager/api/connectors/{key}/{id}: the
// per-row admin read model. Reuses RowConfigs (schema overlaid with stored
// values) and OperationStatesFull, identical to connectorDetailPage. Hidden
// configs (machine-managed OAuth tokens) are dropped, mirroring the templ
// page, and secret values are blanked before serialization.
func (h *Handler) apiConnectorDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	mod, ok := h.connectors.Module(key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown connector"})
		return
	}
	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "connector not found"})
		return
	}
	if !h.canSeeRow(r, user, row.ID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "connector not found"})
		return
	}

	cfgs := h.connectors.RowConfigs(*row)
	fields := make([]configFieldJSON, 0, len(cfgs))
	for _, cfg := range cfgs {
		if cfg.Hidden {
			continue
		}
		f := configFieldJSON{
			Key:         cfg.Key,
			Type:        cfg.Type,
			Value:       cfg.Value,
			Options:     cfg.Options,
			Required:    cfg.Required,
			IsSecret:    cfg.IsSecret,
			HasValue:    cfg.Value != "",
			Description: descJSON(cfg.Description),
			VisibleWhen: cfg.VisibleWhen,
			ColOptions:  cfg.ColOptions,
			EnvOverride: cfg.EnvOverride,
		}
		if cfg.IsSecret {
			f.Value = ""
		}
		fields = append(fields, f)
	}

	states, _ := h.connectors.OperationStatesFull(ctx, row.ID, row.Key)
	ops := make([]connectorOpJSON, 0, len(mod.Operations))
	for _, op := range mod.Operations {
		st := states[op.Key]
		ops = append(ops, connectorOpJSON{
			Key:                  op.Key,
			Name:                 op.Name,
			Description:          op.Description,
			Destructive:          op.Destructive,
			Enabled:              st.Enabled,
			SystemDisabled:       st.SystemDisabled,
			SystemDisabledReason: st.SystemDisabledReason,
		})
	}

	writeJSON(w, http.StatusOK, connectorDetailJSON{
		Key:            mod.Meta.Key,
		Name:           mod.Meta.Name,
		Icon:           mod.Meta.Icon,
		ID:             row.ID,
		Label:          row.Label,
		Disabled:       row.Disabled,
		RateLimitRPM:   row.RateLimitRPM,
		HasHealthCheck: mod.HealthCheck != nil,
		CanConfigure:   h.canConfigureRow(user, row),
		Fields:         fields,
		Operations:     ops,
	})
}

// apiCreateConnectorRow serves POST /manager/api/connectors/{key}/new. It
// reuses createConnectorRow's service calls (Create + owner-tag seeding)
// and returns the new row ID as JSON instead of redirecting.
func (h *Handler) apiCreateConnectorRow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")

	mod, ok := h.connectors.Module(key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown connector"})
		return
	}
	row, err := h.connectors.Create(ctx, key, mod.Meta.Name+" (new)", map[string]string{}, userID(user))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if h.tags != nil && user != nil && !user.IsAdmin() {
		_ = h.tags.CreateOwnerTag(ctx, row.ID, user.ID)
	}
	if h.custom != nil {
		h.custom.EnsureTagsForKey(ctx, key)
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": row.ID})
}

// apiSetConnectorLabel serves POST /manager/api/connectors/{key}/{id}/label.
// Body: {"label":"..."}. Reuses the same permission gate + Update path as
// the templ setConnectorLabel handler.
func (h *Handler) apiSetConnectorLabel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	row, errResp, ok := h.loadConfigurableRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	var body struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	label := strings.TrimSpace(body.Label)
	if label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "label is required"})
		return
	}
	stored := h.connectors.LoadConfigs(*row)
	if err := h.connectors.Update(ctx, row.ID, label, stored, row.Disabled); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"label": label})
}

// apiSetConnectorConfig serves
// POST /manager/api/connectors/{key}/{id}/configs/{configKey}. Body:
// {"value":"..."}. Mirrors setConnectorConfig: gates secret fields behind
// canConfigureSecretField, then persists via Update.
func (h *Handler) apiSetConnectorConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	configKey := r.PathValue("configKey")
	row, errResp, ok := h.loadConfigurableRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	for _, cfg := range h.connectors.RowConfigs(*row) {
		if cfg.Key == configKey && cfg.IsSecret {
			if !h.canConfigureSecretField(user, row) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "secret fields require admin or connector owner"})
				return
			}
			break
		}
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	stored := h.connectors.LoadConfigs(*row)
	stored[configKey] = body.Value
	if err := h.connectors.Update(ctx, row.ID, row.Label, stored, row.Disabled); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// apiToggleConnectorDisabled serves
// POST /manager/api/connectors/{key}/{id}/disable. Flips the row off-switch
// and returns the new state. Mirrors toggleConnectorDisabled.
func (h *Handler) apiToggleConnectorDisabled(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	row, errResp, ok := h.loadVisibleRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	if err := h.connectors.SetDisabled(ctx, row.ID, !row.Disabled); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"disabled": !row.Disabled})
}

// apiDeleteConnectorRow serves
// POST /manager/api/connectors/{key}/{id}/delete. Reuses deleteConnector's
// gate + service calls (Delete + owner-tag cleanup).
func (h *Handler) apiDeleteConnectorRow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	row, errResp, ok := h.loadConfigurableRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	if err := h.connectors.Delete(ctx, row.ID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if h.tags != nil {
		_ = h.tags.DeleteOwnerTag(ctx, row.ID)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// rowErr bundles an HTTP status + message for the small row-loading
// helpers below so each API handler can fail uniformly.
type rowErr struct {
	status int
	msg    string
}

// loadVisibleRow resolves the {key}/{id} path values to a row the caller is
// allowed to see (manage), returning a structured error otherwise. It does
// NOT check write permission — use loadConfigurableRow for mutations.
func (h *Handler) loadVisibleRow(r *http.Request, user *entity.User) (*entity.Connector, rowErr, bool) {
	ctx := r.Context()
	key := r.PathValue("key")
	id := r.PathValue("id")
	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		return nil, rowErr{http.StatusNotFound, "connector not found"}, false
	}
	return row, rowErr{}, true
}

// loadConfigurableRow extends loadVisibleRow with the write gate
// (canConfigureRow), used by every mutating API handler.
func (h *Handler) loadConfigurableRow(r *http.Request, user *entity.User) (*entity.Connector, rowErr, bool) {
	row, errResp, ok := h.loadVisibleRow(r, user)
	if !ok {
		return nil, errResp, false
	}
	if !h.canConfigureRow(user, row) {
		return nil, rowErr{http.StatusForbidden, "not allowed"}, false
	}
	return row, rowErr{}, true
}

// descJSON expands the two-character `\n` escape carried in wick:"desc=…"
// tags into real newlines, matching the templ descText helper so the SPA
// renders multi-line descriptions identically.
func descJSON(s string) string {
	return strings.ReplaceAll(s, `\n`, "\n")
}
