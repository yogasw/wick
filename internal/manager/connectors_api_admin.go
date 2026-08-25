// Package manager — connectors_api_admin.go: JSON twins of the per-row
// admin controls that previously lived only on the form-post templ pages
// (rate limit, duplicate, access policy, session config, operation
// toggles, and account management). Each handler reuses the same service
// calls + permission gates as its templ sibling in connectors.go — the
// only difference is JSON in/out instead of form-post + redirect, so the
// manager SPA can drive them without a page reload.
package manager

import (
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// apiSetConnectorRateLimit serves
// POST /manager/api/connectors/{key}/{id}/rate-limit. Body: {"rpm":N}.
// Mirrors setConnectorRateLimit: requires configure permission, clamps
// negatives to 0.
func (h *Handler) apiSetConnectorRateLimit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	row, errResp, ok := h.loadConfigurableRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	var body struct {
		RPM int `json:"rpm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if body.RPM < 0 {
		body.RPM = 0
	}
	if err := h.connectors.SetRateLimit(ctx, row.ID, body.RPM); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"rate_limit_rpm": body.RPM})
}

// apiDuplicateConnector serves
// POST /manager/api/connectors/{key}/{id}/duplicate. Mirrors
// duplicateConnector: requires only see permission, seeds an owner tag
// for non-admin duplicators, and returns the new row ID.
func (h *Handler) apiDuplicateConnector(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	row, errResp, ok := h.loadVisibleRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	dup, err := h.connectors.Duplicate(ctx, row.ID, userID(user))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if h.tags != nil && user != nil && !user.IsAdmin() {
		// Owner tag so the duplicate is visible to its creator. Tag IDs are
		// read live per request, so it shows up immediately — no cookie
		// re-issue needed.
		if err := h.tags.CreateOwnerTag(ctx, dup.ID, user.ID); err != nil {
			log.Warn().Err(err).Str("row_id", dup.ID).Msg("manager api: create owner tag on duplicate failed")
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": dup.ID})
}

// apiSetConnectorAccessPolicy serves
// POST /manager/api/connectors/{key}/{id}/access-policy. Admin-only, like
// setConnectorAccessPolicy. Body carries the four toggles.
func (h *Handler) apiSetConnectorAccessPolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	row, errResp, ok := h.loadVisibleRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	if user == nil || !user.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin only"})
		return
	}
	var body struct {
		AllowOthersConfigure  bool `json:"allow_others_configure"`
		AllowOthersConnectSSO bool `json:"allow_others_connect_sso"`
		EnableSSO             bool `json:"enable_sso"`
		MultiAccount          bool `json:"multi_account"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if err := h.connectors.SetAccessPolicy(ctx, row.ID, body.AllowOthersConfigure, body.AllowOthersConnectSSO, body.EnableSSO, body.MultiAccount); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// apiSetConnectorSessionConfig serves
// POST /manager/api/connectors/{key}/{id}/session-config. Admin-only and
// only honored for session-config-capable connectors, like
// setConnectorSessionConfig. Body: {"allow_session_config":bool}.
func (h *Handler) apiSetConnectorSessionConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	row, errResp, ok := h.loadVisibleRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	if user == nil || !user.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin only"})
		return
	}
	if !h.connectors.SessionConfigCapable(row.Key) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this connector does not support per-session config override"})
		return
	}
	var body struct {
		AllowSessionConfig bool `json:"allow_session_config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if err := h.connectors.SetSessionConfigAllowed(ctx, row.ID, body.AllowSessionConfig); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"allow_session_config": body.AllowSessionConfig})
}

// apiToggleConnectorOperation serves
// POST /manager/api/connectors/{key}/{id}/operations/{opKey}. Body:
// {"enabled":bool}. Mirrors toggleConnectorOperation: requires configure
// permission and clears any system_disabled lock on explicit enable.
func (h *Handler) apiToggleConnectorOperation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	opKey := r.PathValue("opKey")
	row, errResp, ok := h.loadConfigurableRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if err := h.connectors.SetOperationEnabled(ctx, row.ID, opKey, body.Enabled); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.Enabled {
		_ = h.connectors.ClearSystemDisabled(ctx, row.ID, opKey)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": body.Enabled})
}

// apiBulkToggleOperations serves
// POST /manager/api/connectors/{key}/{id}/operations/bulk. Body:
// {"enabled":bool, "ops":["k1","k2"]}. Empty ops = every declared op,
// mirroring bulkToggleOperations. Requires configure permission.
func (h *Handler) apiBulkToggleOperations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	row, errResp, ok := h.loadConfigurableRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	mod, ok := h.connectors.Module(row.Key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown connector"})
		return
	}
	var body struct {
		Enabled bool     `json:"enabled"`
		Ops     []string `json:"ops"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	targets := body.Ops
	if len(targets) == 0 {
		for _, op := range mod.AllOps() {
			targets = append(targets, op.Key)
		}
	}
	for _, opKey := range targets {
		if err := h.connectors.SetOperationEnabled(ctx, row.ID, opKey, body.Enabled); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if body.Enabled {
			_ = h.connectors.ClearSystemDisabled(ctx, row.ID, opKey)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": body.Enabled, "count": len(targets)})
}

// apiToggleOperationAdminOnly serves
// POST /manager/api/connectors/{key}/{id}/operations/{opKey}/admin-only.
// Admin-only, like toggleOperationAdminOnly. Body: {"admin_only":bool}.
func (h *Handler) apiToggleOperationAdminOnly(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	opKey := r.PathValue("opKey")
	row, errResp, ok := h.loadVisibleRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	if user == nil || !user.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin only"})
		return
	}
	var body struct {
		AdminOnly bool `json:"admin_only"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if err := h.connectors.SetOperationAdminOnly(ctx, row.ID, opKey, body.AdminOnly); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"admin_only": body.AdminOnly})
}

// apiDisconnectAccount serves
// POST /manager/api/connectors/{key}/{id}/accounts/{accountID}/disconnect.
// Mirrors disconnectAccount: admin/owner of the row OR the account's own
// wick user may disconnect.
func (h *Handler) apiDisconnectAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	accountID := r.PathValue("accountID")
	row, acc, errResp, ok := h.loadAccountForRow(r, user, accountID)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	if !h.canManageAccount(user, row, acc) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not allowed"})
		return
	}
	if err := h.connectors.DeleteAccount(ctx, accountID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

// apiSetAccountDisabledOps serves
// POST /manager/api/connectors/{key}/{id}/accounts/{accountID}/ops. Body:
// {"disabled_ops":["k1"]}. Mirrors setAccountDisabledOps — the body
// carries the full disabled-op set, replacing the stored value.
func (h *Handler) apiSetAccountDisabledOps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	accountID := r.PathValue("accountID")
	row, acc, errResp, ok := h.loadAccountForRow(r, user, accountID)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}
	if !h.canManageAccount(user, row, acc) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not allowed"})
		return
	}
	var body struct {
		DisabledOps []string `json:"disabled_ops"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if body.DisabledOps == nil {
		body.DisabledOps = []string{}
	}
	if err := h.connectors.SetAccountDisabledOps(ctx, accountID, body.DisabledOps); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disabled_ops": body.DisabledOps})
}

// loadAccountForRow resolves {key}/{id}/{accountID} to a (row, account)
// pair, gated by canSeeRow and account-belongs-to-row, returning a
// structured error otherwise. Mutation gating (canManageAccount) is the
// caller's job.
func (h *Handler) loadAccountForRow(r *http.Request, user *entity.User, accountID string) (*entity.Connector, *entity.ConnectorAccount, rowErr, bool) {
	row, errResp, ok := h.loadVisibleRow(r, user)
	if !ok {
		return nil, nil, errResp, false
	}
	acc, err := h.connectors.GetAccount(r.Context(), accountID)
	if err != nil || acc.ConnectorID != row.ID {
		return nil, nil, rowErr{http.StatusNotFound, "account not found"}, false
	}
	return row, acc, rowErr{}, true
}

// canManageAccount reports whether the caller may disconnect or edit an
// account: row configurers (admin/owner) OR the account's own wick user.
// Mirrors the inline gate in disconnectAccount / setAccountDisabledOps.
func (h *Handler) canManageAccount(user *entity.User, row *entity.Connector, acc *entity.ConnectorAccount) bool {
	if h.canConfigureRow(user, row) {
		return true
	}
	return user != nil && acc.WickUserID == user.ID
}
