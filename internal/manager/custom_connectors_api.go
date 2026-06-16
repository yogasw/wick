package manager

import (
	"net/http"

	"github.com/rs/zerolog/log"
	customconn "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// customConnectorAPIRoutes wires the JSON surface for the manager SPA
// custom-connector builder (paste / manual / review-edit). It mirrors the
// templ /manager/connectors/custom/* flows but speaks JSON end-to-end,
// reusing the same custom-connector service and the level-1 mutation gate
// (requireDefMutable). The templ routes stay intact for coexistence.
//
// parse + save (create) + save (update) already returned JSON on the
// legacy routes, so those handlers are reused verbatim under /api/. The
// meta/draft reads and the delete/disable/enable mutations are new JSON
// variants of what were HTML page renders or form-post redirects.
func (h *Handler) customConnectorAPIRoutes(mux *http.ServeMux, authMidd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAuth(next)
	}

	// Reads for the builder shell: AI providers + categories for the
	// paste/draft forms, and the stored draft for edit mode.
	mux.Handle("GET /manager/api/connectors/custom/meta", auth(h.apiCustomMeta))
	mux.Handle("GET /manager/api/connectors/custom/{defID}/draft", auth(h.apiCustomDraft))

	// Mutations. parse + save reuse the existing JSON handlers; delete /
	// disable / enable are JSON variants of the redirecting legacy routes.
	mux.Handle("POST /manager/api/connectors/custom/parse", auth(h.customParse))
	mux.Handle("POST /manager/api/connectors/custom/save", auth(h.customSaveNew))
	mux.Handle("POST /manager/api/connectors/custom/{defID}/save", auth(h.customSaveExisting))
	mux.Handle("POST /manager/api/connectors/custom/{defID}/delete", auth(h.apiCustomDelete))
	mux.Handle("POST /manager/api/connectors/custom/{defID}/disable", auth(h.apiCustomSetDisabled(true)))
	mux.Handle("POST /manager/api/connectors/custom/{defID}/enable", auth(h.apiCustomSetDisabled(false)))
}

// apiConnectorReload serves POST /manager/api/connectors/{key}/reload. It
// rebuilds the live module from the stored custom definition, applying any
// pending edits and clearing the "needs reload" state. Custom connectors
// only; available to any authenticated caller — it just applies the
// already-saved definition, no destructive change.
func (h *Handler) apiConnectorReload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := r.PathValue("key")
	if h.custom == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "custom connectors unavailable"})
		return
	}
	defID, ok := h.custom.DefIDForKey(key)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a custom connector"})
		return
	}
	if err := h.custom.Reload(ctx, defID); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiResyncMCPTools serves POST /manager/api/connectors/{key}/resync-tools.
// It re-fetches the custom MCP server's tools/list and swaps the fresh
// operation set in for the whole connector — the op set is definition-level,
// shared by every instance — refreshing the stored connection status. Custom
// MCP connectors only; available to any authenticated caller (the catalog is
// deterministic per connector, so this is not gated to admins/creators).
func (h *Handler) apiResyncMCPTools(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := r.PathValue("key")
	if h.custom == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "custom connectors unavailable"})
		return
	}
	defID, ok := h.custom.DefIDForKey(key)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a custom connector"})
		return
	}
	def, err := h.custom.Store().GetDef(ctx, defID)
	if err != nil || def == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "definition not found"})
		return
	}
	if customconn.ServerIDForDef(def) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not an MCP connector"})
		return
	}
	if err := h.custom.ReloadFor(ctx, defID, ""); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	count := 0
	if mod, ok := h.connectors.Module(key); ok {
		count = len(mod.Operations)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "operations": count})
}

// customMetaResponse is the read model the builder shell consumes before
// rendering the paste tabs and the draft form's category picker. An empty
// ai_providers slice hides the AI parser tab.
type customMetaResponse struct {
	AIProviders []string `json:"ai_providers"`
	Categories  []string `json:"categories"`
}

func (h *Handler) apiCustomMeta(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	writeJSON(w, http.StatusOK, customMetaResponse{
		AIProviders: emptyStrings(h.custom.AIProviderNames()),
		Categories:  emptyStrings(customconn.CategoryNames()),
	})
}

// customDraftResponse carries the editable draft plus the mutation-time
// state the SPA toolbar needs (mcp defs are not editable here; disabled
// state drives the enable/disable toggle label).
type customDraftResponse struct {
	DefID    string            `json:"def_id"`
	Disabled bool              `json:"disabled"`
	MCP      bool              `json:"mcp"`
	ServerID string            `json:"server_id,omitempty"`
	Draft    *customconn.Draft `json:"draft"`
}

// apiCustomDraft returns the stored definition as a review-form draft for
// edit mode. Gated by requireDefMutable (admin ∨ creator); MCP defs have
// no editable ops, signalled via mcp=true so the SPA redirects to the
// server form instead of rendering the draft editor.
func (h *Handler) apiCustomDraft(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	def := h.requireDefMutableJSON(w, r, r.PathValue("defID"))
	if def == nil {
		return
	}
	if serverID := customconn.ServerIDForDef(def); serverID != "" {
		writeJSON(w, http.StatusOK, customDraftResponse{DefID: def.ID, Disabled: def.Disabled, MCP: true, ServerID: serverID})
		return
	}
	draft, err := customDraftFromDef(def)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, customDraftResponse{
		DefID:    def.ID,
		Disabled: def.Disabled,
		Draft:    draft,
	})
}

func (h *Handler) apiCustomDelete(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	def := h.requireDefMutableJSON(w, r, r.PathValue("defID"))
	if def == nil {
		return
	}
	if err := h.custom.Delete(r.Context(), def.ID); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiCustomSetDisabled is the JSON variant of customSetDisabled: toggles a
// definition on/off in place and reports the resulting state instead of
// redirecting.
func (h *Handler) apiCustomSetDisabled(disabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.customNotReady(w, r, true) {
			return
		}
		def := h.requireDefMutableJSON(w, r, r.PathValue("defID"))
		if def == nil {
			return
		}
		if err := h.custom.SetDefDisabled(r.Context(), def.ID, disabled); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		l := log.With().Str("component", "custom-connector").Logger()
		l.Debug().Str("def_id", def.ID).Bool("disabled", disabled).Msg("custom connector disabled toggled")
		writeJSON(w, http.StatusOK, map[string]bool{"disabled": disabled})
	}
}

// requireDefMutableJSON is the JSON twin of requireDefMutable: it loads a
// def and enforces the level-1 mutation rule (admin ∨ creator), but writes
// a JSON 404 instead of an HTML page. Not-found and not-yours are
// indistinguishable on purpose.
func (h *Handler) requireDefMutableJSON(w http.ResponseWriter, r *http.Request, defID string) *entity.CustomConnector {
	ctx := r.Context()
	user := login.GetUser(ctx)
	def, err := h.custom.Store().GetDef(ctx, defID)
	if err != nil || !customconn.CanMutate(def, user) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil
	}
	return def
}

// emptyStrings normalizes a nil slice to an empty one so JSON encodes [],
// not null — the SPA can then iterate without a nil guard.
func emptyStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
