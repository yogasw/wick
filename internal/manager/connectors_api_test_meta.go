package manager

import (
	"net/http"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// testInputJSON is the per-operation input field schema the SPA test
// runner renders one widget per. It is the JSON projection of the input
// entity.Config the legacy testInputField templ rendered: key, type,
// required, and the description hint.
type testInputJSON struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// testOpJSON carries one operation's identity plus its input schema so
// the SPA can build the op dropdown and the matching input form without
// a second round trip per op.
type testOpJSON struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Destructive bool            `json:"destructive"`
	Input       []testInputJSON `json:"input"`
}

// testAccountJSON is one OAuth-connected account the test runner offers
// in its "Run as" dropdown. DisplayName drives the option label.
type testAccountJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// testMetaJSON is the shape served at
// GET /manager/api/connectors/{key}/{id}/test-meta: the connector
// identity, every operation with its input schema, and any connected
// accounts. It is the read model the SPA ConnectorTest page consumes to
// render its op picker + input form. Execution still POSTs to the
// existing /manager/connectors/{key}/{id}/test JSON endpoint.
type testMetaJSON struct {
	Key      string            `json:"key"`
	Name     string            `json:"name"`
	Icon     string            `json:"icon"`
	ID       string            `json:"id"`
	Label    string            `json:"label"`
	Ops      []testOpJSON      `json:"ops"`
	Accounts []testAccountJSON `json:"accounts"`
}

// apiConnectorTestMeta serves the test-runner metadata for one connector
// row: the operation list with each op's input schema plus connected
// accounts. Visibility reuses loadVisibleRow, identical to the templ
// connectorTestPage gate. The op input schema was previously only
// available to the server-rendered templ; this exposes it to the SPA.
func (h *Handler) apiConnectorTestMeta(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")

	mod, ok := h.connectors.Module(key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown connector"})
		return
	}
	row, errResp, ok := h.loadVisibleRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}

	ops := make([]testOpJSON, 0, len(mod.AllOps()))
	for _, op := range mod.AllOps() {
		ins := make([]testInputJSON, 0, len(op.Input))
		for _, in := range op.Input {
			ins = append(ins, testInputJSON{
				Key:         in.Key,
				Type:        in.Type,
				Required:    in.Required,
				Description: descJSON(in.Description),
			})
		}
		ops = append(ops, testOpJSON{
			Key:         op.Key,
			Name:        op.Name,
			Description: op.Description,
			Destructive: op.Destructive,
			Input:       ins,
		})
	}

	accs, _ := h.connectors.ListAccounts(ctx, row.ID)
	accounts := make([]testAccountJSON, 0, len(accs))
	for _, a := range accs {
		accounts = append(accounts, testAccountJSON{ID: a.ID, DisplayName: a.DisplayName})
	}

	writeJSON(w, http.StatusOK, testMetaJSON{
		Key:      mod.Meta.Key,
		Name:     mod.Meta.Name,
		Icon:     mod.Meta.Icon,
		ID:       row.ID,
		Label:    row.Label,
		Ops:      ops,
		Accounts: accounts,
	})
}

// apiTestConnectorOperation is the JSON test-runner exec endpoint for the
// SPA. It delegates to testConnectorOperation, which already speaks JSON
// (it backs the legacy /manager/connectors/{key}/{id}/test route); this
// alias keeps the SPA on the /manager/api/connectors/... surface so the
// api client's connector base is uniform.
func (h *Handler) apiTestConnectorOperation(w http.ResponseWriter, r *http.Request) {
	h.testConnectorOperation(w, r)
}

// _ keeps the entity import alive for the account projection above.
var _ entity.ConnectorAccount
