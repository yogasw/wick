package manager

import (
	"net/http"

	customconn "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/internal/login"
)

// customMCPServerAPIRoutes wires the JSON read used by the manager SPA's
// MCP server form in edit mode. The test/save/oauth endpoints already
// speak JSON on the legacy /manager/connectors/custom/mcp-servers/*
// routes and are reused verbatim by the SPA; only the edit-page prefill
// was an HTML render, so it gets a JSON twin here. The templ routes stay
// intact for coexistence during the migration.
func (h *Handler) customMCPServerAPIRoutes(mux *http.ServeMux, authMidd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAuth(next)
	}
	mux.Handle("GET /manager/api/connectors/custom/mcp-servers/edit", auth(h.apiMCPServerForm))
}

// mcpToolRow is one entry of the exclude-list catalog in the JSON
// prefill — the live tools/list slimmed for display.
type mcpToolRow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// mcpServerInfo carries the definition-level state the SPA toolbar needs
// in edit mode (the danger zone + enable/disable toggle). Nil on the new
// form.
type mcpServerInfo struct {
	DefID    string `json:"def_id"`
	Disabled bool   `json:"disabled"`
}

// mcpServerFormResponse is the edit-mode prefill: the stored form
// (secret values stay as their wick_enc_ tokens), the live tool catalog
// for the exclude list (empty when the server was unreachable), and the
// definition-level controls.
type mcpServerFormResponse struct {
	ID    string                 `json:"id"`
	Form  *customconn.ServerForm `json:"form"`
	Tools []mcpToolRow           `json:"tools"`
	Info  *mcpServerInfo         `json:"info"`
}

// apiMCPServerForm is the JSON twin of customMCPServerEditPage: it loads
// a stored server row, maps it back into the form payload, probes the
// live tool catalog (best-effort), and resolves the definition-level
// controls. Editing a server mutates its definition, so the level-1
// mutation rule (admin ∨ creator) is enforced — not-found and not-yours
// are indistinguishable on purpose.
func (h *Handler) apiMCPServerForm(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	ctx := r.Context()
	user := login.GetUser(ctx)
	id := r.URL.Query().Get("id")
	srv, err := h.custom.Store().GetServer(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	form, err := serverFormFromRow(srv)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := mcpServerFormResponse{ID: srv.ID, Form: form, Tools: []mcpToolRow{}}
	// Live tools/list so the exclude list shows the server's current
	// surface without forcing a manual test first. Best-effort — a down
	// server still renders the form (tools stay empty until a test).
	if res, perr := h.custom.ProbeStored(ctx, srv.ID, customSSOClaims(r)); perr == nil && res.OK {
		for _, t := range res.Tools {
			resp.Tools = append(resp.Tools, mcpToolRow{Name: t.Name, Description: t.Description})
		}
	}
	// Editing a server mutates its definition — level-1 rule.
	if def := h.custom.DefForServer(ctx, srv.ID); def != nil {
		if !customconn.CanMutate(def, user) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		resp.Info = &mcpServerInfo{DefID: def.ID, Disabled: def.Disabled}
		form.Icon = def.Icon
		form.Description = def.Description
	}
	writeJSON(w, http.StatusOK, resp)
}
