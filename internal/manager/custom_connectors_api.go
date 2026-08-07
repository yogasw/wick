package manager

import (
	"encoding/json"
	"net/http"
	"strings"

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
	mux.Handle("POST /manager/api/connectors/custom/{defID}/rename", auth(h.apiCustomRename))
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
//
// An optional instance_id runs the probe under that instance's own OAuth
// account. Servers may expose a different tools/list per account, so a
// resync triggered from an instance row must not probe as some other row:
// the caller that owns the credentials is the one that gets asked.
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
	// A probe instance must belong to THIS connector and be visible to the
	// caller — otherwise the id is a side door into another row's token.
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID != "" {
		row, err := h.connectors.Get(ctx, instanceID)
		if err != nil || row.Key != key || !h.canSeeRow(r, login.GetUser(ctx), row.ID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "instance not found"})
			return
		}
	}
	if err := h.custom.ReloadFor(ctx, defID, instanceID); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	count := 0
	if mod, ok := h.connectors.Module(key); ok {
		count = len(mod.AllOps())
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

// apiTestInstanceAuth serves
// POST /manager/api/connectors/{key}/{id}/test-auth: an auth check scoped
// to ONE instance row. Under the oauth scheme each row carries its own
// token, so this is the only way to tell whether a specific account still
// works — a connector-wide probe would answer for whichever row it happened
// to borrow credentials from.
//
// Always 200 with an ok flag: a refused or expired token is a verdict about
// the credentials, not an HTTP failure of this endpoint.
func (h *Handler) apiTestInstanceAuth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.custom == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "custom connectors unavailable"})
		return
	}
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	row, err := h.connectors.Get(ctx, r.PathValue("id"))
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "instance not found"})
		return
	}
	res, err := h.custom.ProbeInstance(ctx, row.ID, customSSOClaims(r))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         res.OK,
		"error":      res.Error,
		"latency_ms": res.LatencyMs,
		"tools":      len(res.Tools),
	})
}

// apiCustomRename serves POST /manager/api/connectors/custom/{defID}/rename.
// Display-name only: the connector key is immutable, so existing instances,
// tags, and MCP tool ids keep working. Reloads so the new name serves at
// once rather than waiting on the dirty banner.
func (h *Handler) apiCustomRename(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	def := h.requireDefMutableJSON(w, r, r.PathValue("defID"))
	if def == nil {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if err := h.custom.Rename(r.Context(), def.ID, body.Name); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if err := h.custom.Reload(r.Context(), def.ID); err != nil {
		// The rename is committed; only the live swap failed. Report OK and
		// let the dirty banner drive the reload rather than losing the edit.
		l := log.With().Str("component", "custom-connector").Logger()
		l.Warn().Err(err).Str("def_id", def.ID).Msg("rename saved but reload failed")
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true", "name": strings.TrimSpace(body.Name)})
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
