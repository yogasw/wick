// Package manager — connectors_api_account.go: the per-connected-account
// read model and its one mutation.
//
// Purpose: An account is its own thing to administer, not a sub-row of the
// instance page. It has operations that may differ from the instance's, a
// history of what it actually did, a Test runner pinned to its identity,
// and the two lifecycle actions (re-connect, disconnect). This file serves
// exactly that, so a person who may not configure the instance still has a
// page for the account that is theirs.
//
// Caller:   the manager SPA's AccountDetail page
// Dependencies: connectors.Service (accounts, op states, overrides)
// Main Functions:
//   - apiAccountDetail()        — GET the account + its resolved operations
//   - apiSetAccountOpOverride() — POST one operation's inherit/on/off state
//
// Side Effects: the POST writes the account's override map.
package manager

import (
	"encoding/json"
	"net/http"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/login"
)

// accountOpJSON is one operation as it applies to one account.
//
// Four fields rather than one boolean, because "off" has three different
// causes an operator has to tell apart: the instance says off and this
// account follows it, the account itself says off, or the health check has
// locked it because the credential lacks the permission. A checkbox can
// only say "off".
type accountOpJSON struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Destructive bool   `json:"destructive"`
	// Enabled is what applies to this account right now.
	Enabled bool `json:"enabled"`
	// State is how that was decided: "inherit", "on" or "off".
	State string `json:"state"`
	// Inherited is what the instance says — the value this operation falls
	// back to when the override is cleared. Shown next to an override so
	// "override off" and "inherited off" do not look identical.
	Inherited bool `json:"inherited"`
	// SystemDisabled is the health-check lock on the instance. It outranks
	// any override, so the UI locks the control rather than offering a
	// switch that cannot take effect.
	SystemDisabled bool   `json:"system_disabled"`
	Reason         string `json:"system_disabled_reason"`
}

// accountDetailJSON is the shape served at
// GET /manager/api/connectors/{key}/{id}/accounts/{accountID}.
type accountDetailJSON struct {
	ConnectorKey  string `json:"connector_key"`
	ConnectorName string `json:"connector_name"`
	RowID         string `json:"row_id"`
	RowLabel      string `json:"row_label"`
	AccountID     string `json:"account_id"`
	DisplayName   string `json:"display_name"`
	// CanManage gates the write controls. A viewer who may not manage the
	// account still gets the page — read-only — because seeing which
	// operations apply to an identity is not a privileged question.
	CanManage bool `json:"can_manage"`
	// ReconnectURL is the OAuth start target, empty when the caller may not
	// re-consent (same gate as the instance's Connect button).
	ReconnectURL string          `json:"reconnect_url"`
	Ops          []accountOpJSON `json:"ops"`
}

func (h *Handler) apiAccountDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	accountID := r.PathValue("accountID")

	mod, ok := h.connectors.Module(key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown connector"})
		return
	}
	row, acc, errResp, ok := h.loadAccountForRow(r, user, accountID)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}

	states, err := h.connectors.OperationStatesFull(ctx, row.ID, row.Key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Walk the module's declared operations, not the state map: the map has
	// no order and no display names, and an operation the module declares
	// but the row has never stored a toggle for still has to appear.
	all := mod.AllOps()
	keys := make([]string, 0, len(all))
	for _, op := range all {
		keys = append(keys, op.Key)
	}
	resolved := connectors.ResolveAccountOps(acc, states, keys)
	byKey := make(map[string]connectors.AccountOpState, len(resolved))
	for _, st := range resolved {
		byKey[st.Key] = st
	}

	out := accountDetailJSON{
		ConnectorKey:  mod.Meta.Key,
		ConnectorName: mod.Meta.Name,
		RowID:         row.ID,
		RowLabel:      row.Label,
		AccountID:     acc.ID,
		DisplayName:   acc.DisplayName,
		CanManage:     h.canManageAccount(user, row, acc),
		Ops:           make([]accountOpJSON, 0, len(all)),
	}
	if mod.OAuth != nil {
		if oa := h.rowOAuthJSON(mod, *row, user); oa != nil {
			out.ReconnectURL = oa.StartURL
		}
	}
	for _, op := range all {
		st := byKey[op.Key]
		state := connectors.AccountOpInherit
		if st.Overridden {
			state = connectors.AccountOpOff
			if st.Enabled {
				state = connectors.AccountOpOn
			}
		}
		out.Ops = append(out.Ops, accountOpJSON{
			Key:            op.Key,
			Name:           op.Name,
			Description:    op.Description,
			Destructive:    op.Destructive,
			Enabled:        st.Enabled,
			State:          state,
			Inherited:      st.Inherited,
			SystemDisabled: st.SystemDisabled,
			Reason:         st.Reason,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// apiSetAccountOpOverride serves
// POST /manager/api/connectors/{key}/{id}/accounts/{accountID}/ops/{opKey}
// with {"state":"inherit"|"on"|"off"}.
//
// One operation per call rather than a whole map: the control is per-row in
// the UI, and a whole-map write turns a stale tab into a silent revert of
// everything somebody else changed meanwhile.
func (h *Handler) apiSetAccountOpOverride(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	accountID := r.PathValue("accountID")
	opKey := r.PathValue("opKey")

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
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if err := h.connectors.SetAccountOpOverride(ctx, accountID, opKey, body.State); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": opKey, "state": body.State})
}
