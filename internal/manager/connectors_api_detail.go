package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
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
	DefID       string             `json:"def_id,omitempty"`
	MCP         bool               `json:"mcp"`
	MCPStatus   string             `json:"mcp_status,omitempty"`
	NeedsReload bool               `json:"needs_reload"`
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
	AdminOnly            bool   `json:"admin_only"`
	Category             string `json:"category"`
}

// connectorCategoryJSON is the section header the operations table renders
// per category: a stable key plus its human-facing title and description.
type connectorCategoryJSON struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// connectorAccountJSON is the per-account read model for the detail page's
// Accounts section: identity + the per-account disabled-op list. The access
// token is never serialized.
type connectorAccountJSON struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	WickUserID  string   `json:"wick_user_id"`
	DisabledOps []string `json:"disabled_ops"`
	CanManage   bool     `json:"can_manage"`
}

// connectorOAuthJSON mirrors the subset of Module.OAuth the SPA needs to
// render the access-policy SSO toggles + the Connect button. Nil on the
// wire when the connector has no OAuth support.
type connectorOAuthJSON struct {
	DisplayName string `json:"display_name"`
	StartURL    string `json:"start_url"`
}

// connectorDetailJSON is the shape served at
// GET /manager/api/connectors/{key}/{id}: the row identity, the connector
// type metadata, the visible config fields, and the operations table.
type connectorDetailJSON struct {
	Key            string                  `json:"key"`
	Name           string                  `json:"name"`
	Icon           string                  `json:"icon"`
	ID             string                  `json:"id"`
	Label          string                  `json:"label"`
	Disabled       bool                    `json:"disabled"`
	RateLimitRPM   int                     `json:"rate_limit_rpm"`
	HasHealthCheck bool                    `json:"has_health_check"`
	CanConfigure   bool                    `json:"can_configure"`
	IsAdmin        bool                    `json:"is_admin"`
	Fields         []configFieldJSON       `json:"fields"`
	Operations     []connectorOpJSON       `json:"operations"`
	Categories     []connectorCategoryJSON `json:"categories"`
	Accounts       []connectorAccountJSON  `json:"accounts"`
	OAuth          *connectorOAuthJSON     `json:"oauth"`
	// Access policy (admin-controlled). Surfaced so the SPA can render
	// the toggles + decide whether the Connect button is offered.
	EnableSSO             bool `json:"enable_sso"`
	MultiAccount          bool `json:"multi_account"`
	AllowOthersConnectSSO bool `json:"allow_others_connect_sso"`
	AllowOthersConfigure  bool `json:"allow_others_configure"`
	// Session config: capability is module-level, allowed is per-instance.
	SessionConfigCapable bool `json:"session_config_capable"`
	SessionConfigAllowed bool `json:"session_config_allowed"`
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

	customInfo := h.customDefInfo(ctx, key, user)
	defID := ""
	if customInfo != nil {
		defID = customInfo.DefID
	}
	mcp, mcpStatus := h.mcpConnectorInfo(ctx, key)
	out := connectorListJSON{
		Key:         mod.Meta.Key,
		Name:        mod.Meta.Name,
		Description: mod.Meta.Description,
		Icon:        mod.Meta.Icon,
		Fixed:       mod.Meta.Fixed,
		OpCount:     len(mod.AllOps()),
		Custom:      customInfo != nil,
		DefID:       defID,
		MCP:         mcp,
		MCPStatus:   mcpStatus,
		NeedsReload: h.connectorNeedsReload(ctx, key),
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
	ops := make([]connectorOpJSON, 0)
	for _, cat := range mod.Operations {
		for _, op := range cat.Ops {
			st := states[op.Key]
			ops = append(ops, connectorOpJSON{
				Key:                  op.Key,
				Name:                 op.Name,
				Description:          op.Description,
				Destructive:          op.Destructive,
				Enabled:              st.Enabled,
				SystemDisabled:       st.SystemDisabled,
				SystemDisabledReason: st.SystemDisabledReason,
				AdminOnly:            st.AdminOnly,
				Category:             cat.Title,
			})
		}
	}
	categories := buildCategoryJSON(mod)

	isAdmin := user != nil && user.IsAdmin()
	canConfigure := h.canConfigureRow(user, row)

	accounts := make([]connectorAccountJSON, 0)
	for _, acc := range h.accountsForRow(ctx, row.ID) {
		accounts = append(accounts, connectorAccountJSON{
			ID:          acc.ID,
			DisplayName: acc.DisplayName,
			WickUserID:  acc.WickUserID,
			DisabledOps: accountDisabledOpKeys(&acc),
			CanManage:   canConfigure || (user != nil && acc.WickUserID == user.ID),
		})
	}

	var oauthJSON *connectorOAuthJSON
	if mod.OAuth != nil {
		oauthJSON = &connectorOAuthJSON{DisplayName: mod.OAuth.DisplayName}
		// Connect is only offered when SSO is enabled, the caller may
		// connect, and client_id is configured — same gate as the list page.
		if row.EnableSSO && h.canConnectSSO(user, row) {
			if strings.TrimSpace(h.connectors.LoadConfigs(*row)["client_id"]) != "" {
				oauthJSON.StartURL = "/manager/connectors/" + mod.Meta.Key + "/oauth/start?connector_id=" + row.ID
			}
		}
	}

	writeJSON(w, http.StatusOK, connectorDetailJSON{
		Key:                   mod.Meta.Key,
		Name:                  mod.Meta.Name,
		Icon:                  mod.Meta.Icon,
		ID:                    row.ID,
		Label:                 row.Label,
		Disabled:              row.Disabled,
		RateLimitRPM:          row.RateLimitRPM,
		HasHealthCheck:        mod.HealthCheck != nil,
		CanConfigure:          canConfigure,
		IsAdmin:               isAdmin,
		Fields:                fields,
		Operations:            ops,
		Categories:            categories,
		Accounts:              accounts,
		OAuth:                 oauthJSON,
		EnableSSO:             row.EnableSSO,
		MultiAccount:          row.MultiAccount,
		AllowOthersConnectSSO: row.AllowOthersConnectSSO,
		AllowOthersConfigure:  row.AllowOthersConfigure,
		SessionConfigCapable:  mod.AllowSessionConfig,
		SessionConfigAllowed:  row.AllowSessionConfig,
	})
}

// buildCategoryJSON projects the section headers the detail page renders,
// one per titled category that has at least one operation, in declaration
// order. mod.Operations is the canonical []connector.Category; an op row's
// Category field equals its section Title, which the UI keys its grouping
// off (so the JSON key doubles as the title). Untitled categories carry the
// ungrouped ops and are omitted here — the UI renders those without a header.
func buildCategoryJSON(mod connector.Module) []connectorCategoryJSON {
	out := make([]connectorCategoryJSON, 0, len(mod.Operations))
	for _, c := range mod.Operations {
		if c.Title == "" || len(c.Ops) == 0 {
			continue
		}
		out = append(out, connectorCategoryJSON{Key: c.Title, Title: c.Title, Description: c.Description})
	}
	return out
}

// accountsForRow lists a row's connected accounts, swallowing the (rare)
// repo error into an empty slice so the detail read stays best-effort —
// account management has its own endpoints that surface failures.
func (h *Handler) accountsForRow(ctx context.Context, rowID string) []entity.ConnectorAccount {
	accs, err := h.connectors.ListAccounts(ctx, rowID)
	if err != nil {
		return nil
	}
	return accs
}

// accountDisabledOpKeys returns the sorted disabled-op keys for an account
// as a slice, for the JSON projection.
func accountDisabledOpKeys(acc *entity.ConnectorAccount) []string {
	m := connectors.AccountDisabledOps(acc)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
