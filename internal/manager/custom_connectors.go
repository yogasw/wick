package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
	customconn "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager/view"
	"github.com/yogasw/wick/internal/pkg/ui"
)

// SetCustomConnectors late-binds the custom-connector service. The
// builder routes under /manager/connectors/custom/* render only when
// this is non-nil; the connectors index additionally shows the
// "+ New connector" dropdown and the "Custom" card badges.
func (h *Handler) SetCustomConnectors(svc *customconn.Service) {
	h.custom = svc
}

// customConnectorRoutes wires the builder surface for custom connector
// definitions and their MCP servers. Every pattern
// keeps the literal "custom" first segment so it wins over the
// /manager/connectors/{key}/... wildcards. The mcp-server edit/import/
// delete routes carry the server id as a query/body parameter instead
// of a path segment — a `custom/mcp-servers/{id}/<verb>` pattern would
// conflict with `{key}/{id}/accounts/{accountID}` on Go 1.22's ServeMux
// (neither is more specific, registration panics).
func (h *Handler) customConnectorRoutes(mux *http.ServeMux, authMidd *login.Middleware) {
	// Ownership contract level 1: every approved user may CREATE a
	// definition; mutating one (edit/save/reload/disable/delete) is
	// gated in-handler to admin ∨ creator via requireDefMutable.
	authOnly := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAuth(next)
	}

	// JSON surface for the manager SPA builder (paste / manual / review).
	// Mirrors the templ flows below but speaks JSON; the templ routes stay
	// for coexistence during the migration.
	h.customConnectorAPIRoutes(mux, authMidd)
	// JSON read for the SPA MCP server form (edit-mode prefill). The
	// test/save/oauth endpoints below already speak JSON and are reused.
	h.customMCPServerAPIRoutes(mux, authMidd)

	// Definition builder flows. The builder pages render the SPA shell; the
	// SPA reads the JSON twins (apiCustomMeta / apiCustomDraft). Every
	// POST/mutation route stays live.
	mux.Handle("GET /manager/connectors/custom/new/paste", authOnly(http.HandlerFunc(h.serveSPAShell)))
	mux.Handle("POST /manager/connectors/custom/parse", authOnly(h.customParse))
	mux.Handle("GET /manager/connectors/custom/new/manual", authOnly(http.HandlerFunc(h.serveSPAShell)))
	mux.Handle("GET /manager/connectors/custom/new/review", authOnly(http.HandlerFunc(h.serveSPAShell)))
	mux.Handle("POST /manager/connectors/custom/save", authOnly(h.customSaveNew))
	mux.Handle("GET /manager/connectors/custom/{defID}/edit", authOnly(http.HandlerFunc(h.serveSPAShell)))
	mux.Handle("POST /manager/connectors/custom/{defID}/save", authOnly(h.customSaveExisting))
	mux.Handle("POST /manager/connectors/custom/{defID}/reload", authOnly(h.customReload))
	mux.Handle("POST /manager/connectors/custom/{defID}/disable", authOnly(h.customSetDisabled(true)))
	mux.Handle("POST /manager/connectors/custom/{defID}/enable", authOnly(h.customSetDisabled(false)))
	mux.Handle("POST /manager/connectors/custom/{defID}/delete", authOnly(h.customDelete))

	// MCP servers — /mcp-servers is the register form; saving creates the
	// connector directly (one server = one connector, every listed tool
	// exposed minus the exclude list). No list page, no import picker.
	// Deleting the connector definition cascades to the server row. The
	// register/edit form GET pages render the SPA shell (which reads the
	// JSON edit twin); test/save/oauth below already speak JSON and are reused.
	mux.Handle("GET /manager/connectors/custom/mcp-servers", authOnly(http.HandlerFunc(h.serveSPAShell)))
	mux.Handle("GET /manager/connectors/custom/mcp-servers/new", authOnly(http.HandlerFunc(h.serveSPAShell)))
	mux.Handle("GET /manager/connectors/custom/mcp-servers/edit", authOnly(http.HandlerFunc(h.serveSPAShell)))
	mux.Handle("POST /manager/connectors/custom/mcp-servers/test", authOnly(h.customMCPServerTest))
	mux.Handle("POST /manager/connectors/custom/mcp-servers/save", authOnly(h.customMCPServerSave))

	// OAuth (MCP authorization spec): browser login per account. The
	// register form runs it in a popup (login rides the form session
	// until save); instance pages run it full-page to attach an account
	// to an existing row.
	mux.Handle("POST /manager/connectors/custom/mcp-servers/oauth/start", authOnly(h.customMCPOAuthStart))
	mux.Handle("GET /manager/connectors/custom/mcp-servers/oauth/status", authOnly(h.customMCPOAuthStatus))
	mux.Handle("GET /manager/connectors/custom/mcp-servers/oauth/callback", authOnly(h.customMCPOAuthCallback))
	mux.Handle("POST /manager/connectors/custom/mcp-servers/connect", authOnly(h.customMCPConnectInstance))
}

// customNotReady guards every builder handler: the custom-connector
// service is late-bound at boot, so a nil service means the feature is
// not wired on this deployment — render/return 404.
func (h *Handler) customNotReady(w http.ResponseWriter, r *http.Request, asJSON bool) bool {
	if h.custom != nil {
		return false
	}
	if asJSON {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "custom connectors are not enabled"})
	} else {
		ui.RenderNotFound(w, r, login.GetUser(r.Context()), http.StatusNotFound)
	}
	return true
}

// requireDefMutable loads a def and enforces the level-1 mutation rule
// (admin ∨ creator). Returns nil after writing a 404 — not-found and
// not-yours are indistinguishable on purpose.
func (h *Handler) requireDefMutable(w http.ResponseWriter, r *http.Request, defID string) *entity.CustomConnector {
	ctx := r.Context()
	user := login.GetUser(ctx)
	def, err := h.custom.Store().GetDef(ctx, defID)
	if err != nil || !customconn.CanMutate(def, user) {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return nil
	}
	return def
}

// customDefInfo resolves the custom-definition decoration for a
// connector key: nil for built-ins (or when the feature is not wired),
// and nil for callers who may not mutate the def — definition controls
// render only for admin ∨ creator; everyone else sees plain pages.
func (h *Handler) customDefInfo(ctx context.Context, key string, user *entity.User) *view.CustomDefInfo {
	if h.custom == nil {
		return nil
	}
	defID, ok := h.custom.DefIDForKey(key)
	if !ok {
		return nil
	}
	def, err := h.custom.Store().GetDef(ctx, defID)
	if err != nil || !customconn.CanMutate(def, user) {
		return nil
	}
	label := "Manual"
	switch def.Source {
	case entity.CustomConnectorSourceCurl:
		label = "cURL"
	case entity.CustomConnectorSourceMCP:
		label = "MCP"
	}
	info := &view.CustomDefInfo{
		DefID:       def.ID,
		SourceLabel: label,
		Dirty:       h.custom.IsDirty(def),
		Disabled:    def.Disabled,
	}
	if serverID := customconn.ServerIDForDef(def); serverID != "" {
		info.MCP = true
		if srv, err := h.custom.Store().GetServer(ctx, serverID); err == nil {
			info.Tested = srv.LastTestAt != nil
			info.Connected = srv.LastTestOK
			info.OAuth = srv.AuthScheme == "oauth"
		}
	}
	return info
}

// customSSOClaims builds the caller identity forwarded to MCP servers
// using the sso auth scheme from the session user.
func customSSOClaims(r *http.Request) *customconn.SSOClaims {
	user := login.GetUser(r.Context())
	if user == nil {
		return nil
	}
	return &customconn.SSOClaims{
		Subject: user.ID,
		Email:   user.Email,
		Name:    user.Name,
		Groups:  login.GetUserTagIDs(r.Context()),
	}
}

// ── Definition builder ───────────────────────────────────────────────
//
// The templ builder pages (paste / manual / review / edit) and the MCP
// register/edit form pages were removed in the SPA cutover. Their GET
// routes 302 to the SPA, which reads apiCustomMeta / apiCustomDraft and
// the MCP edit twin. The parse / save / reload / disable / delete and the
// MCP test / save / oauth mutation handlers below stay live.

func (h *Handler) customParse(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	var body struct {
		Parser   string `json:"parser"`
		Provider string `json:"provider"`
		Paste    string `json:"paste"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	d, err := h.custom.ParsePaste(r.Context(), body.Parser, body.Provider, body.Paste)
	if err != nil {
		l := log.With().Str("component", "custom-connector").Logger()
		l.Debug().Err(err).Str("parser", body.Parser).Msg("paste parse failed")
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *Handler) customSaveNew(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	user := login.GetUser(r.Context())
	var d customconn.Draft
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	def, instanceID, err := h.custom.SaveNew(r.Context(), &d, userID(user))
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	l := log.With().Str("component", "custom-connector").Logger()
	l.Debug().Str("key", def.Key).Str("instance_id", instanceID).Msg("custom connector saved")
	// No instance is auto-seeded — land on the connector page where
	// "+ New row" creates the first one.
	redirect := "/manager/connectors/" + def.Key
	if instanceID != "" {
		redirect += "/" + instanceID
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": redirect})
}

// customDraftFromDef rebuilds the review-form draft from a stored
// definition so the edit page prefills with exactly what will persist.
func customDraftFromDef(def *entity.CustomConnector) (*customconn.Draft, error) {
	fields, err := customconn.ParseFields(def.Configs)
	if err != nil {
		return nil, err
	}
	ops, err := customconn.ParseOps(def.Ops)
	if err != nil {
		return nil, err
	}
	meta := customconn.ParseSourceMeta(def.SourceMeta)
	return &customconn.Draft{
		Key:                def.Key,
		Name:               def.Name,
		Description:        def.Description,
		Icon:               def.Icon,
		Source:             string(def.Source),
		Category:           meta.Category,
		Single:             def.SingleInstance,
		AllowSessionConfig: def.AllowSessionConfig,
		HealthOp:           meta.HealthOp,
		HealthExpect:       meta.HealthExpect,
		Configs:            fields,
		Ops:                ops,
	}, nil
}

func (h *Handler) customSaveExisting(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	def := h.requireDefMutable(w, r, r.PathValue("defID"))
	if def == nil {
		return
	}
	var d customconn.Draft
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if err := h.custom.Update(r.Context(), def.ID, &d); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	// Apply immediately — swap the live module so the edit takes effect
	// without a separate manual Reload step. In-flight calls finish on the
	// old closures; new calls see the new schema.
	if err := h.custom.Reload(r.Context(), def.ID); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reload_error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) customReload(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, false) {
		return
	}
	ctx := r.Context()
	def := h.requireDefMutable(w, r, r.PathValue("defID"))
	if def == nil {
		return
	}
	// instance_id scopes an MCP re-sync to that row's account (oauth
	// servers may expose different tools per account) and routes the
	// redirect back to the page the click came from.
	instanceID := r.FormValue("instance_id")
	if err := h.custom.ReloadFor(ctx, def.ID, instanceID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if instanceID != "" {
		http.Redirect(w, r, "/manager/connectors/"+def.Key+"/"+instanceID, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+def.Key, http.StatusFound)
}

// customSetDisabled toggles a definition on/off in place: disabled defs
// keep their cards, instance rows, and pages but serve zero operations
// until re-enabled (MCP defs re-probe tools/list on enable).
func (h *Handler) customSetDisabled(disabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.customNotReady(w, r, false) {
			return
		}
		ctx := r.Context()
		def := h.requireDefMutable(w, r, r.PathValue("defID"))
		if def == nil {
			return
		}
		if err := h.custom.SetDefDisabled(ctx, def.ID, disabled); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/manager/connectors/"+def.Key, http.StatusFound)
	}
}

func (h *Handler) customDelete(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, false) {
		return
	}
	def := h.requireDefMutable(w, r, r.PathValue("defID"))
	if def == nil {
		return
	}
	if err := h.custom.Delete(r.Context(), def.ID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/manager/connectors", http.StatusFound)
}

// ── MCP servers ──────────────────────────────────────────────────────
//
// The templ MCP register/edit form pages were removed in the SPA cutover;
// their GET routes 302 to the SPA, which reads the JSON edit twin
// (apiMCPServerForm in custom_mcp_api.go). The test/save/oauth/connect
// mutation handlers below stay live.

// serverFormFromRow maps a stored server row back into the form
// payload. Secret values stay as their wick_enc_ tokens — the form
// passes them through unchanged unless the admin replaces them.
func serverFormFromRow(srv *entity.CustomConnectorMCPServer) (*customconn.ServerForm, error) {
	authHeaders, err := customconn.ParseHeaderRows(srv.AuthHeaders)
	if err != nil {
		return nil, err
	}
	headers, err := customconn.ParseHeaderRows(srv.Headers)
	if err != nil {
		return nil, err
	}
	// AuthExtra is scheme-shaped: SSOExtra for sso, the OAuth client
	// material for oauth.
	var sso customconn.SSOExtra
	var oauth customconn.OAuthFormExtra
	switch srv.AuthScheme {
	case "oauth":
		oauth = customconn.ParseOAuthFormExtra(srv.AuthExtra)
	default:
		if strings.TrimSpace(srv.AuthExtra) != "" {
			_ = json.Unmarshal([]byte(srv.AuthExtra), &sso)
		}
	}
	excluded := []string{}
	if strings.TrimSpace(srv.ExcludedTools) != "" {
		_ = json.Unmarshal([]byte(srv.ExcludedTools), &excluded)
	}
	return &customconn.ServerForm{
		Label:       srv.Label,
		URL:         srv.URL,
		AuthScheme:  srv.AuthScheme,
		AuthSecret:  srv.AuthSecret,
		AuthHeaders: authHeaders,
		Headers:     headers,
		SSO:         sso,
		OAuth:       oauth,
		Excluded:    excluded,
	}, nil
}

func (h *Handler) customMCPServerTest(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	var form customconn.ServerForm
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	var claims *customconn.SSOClaims
	if form.AuthScheme == "sso" {
		claims = customSSOClaims(r)
	}
	res := h.custom.TestServer(r.Context(), &form, claims)
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) customMCPServerSave(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	var body struct {
		Form     customconn.ServerForm `json:"form"`
		TestedOK bool                  `json:"tested_ok"`
		ID       string                `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	user := login.GetUser(r.Context())
	if body.ID != "" {
		// Editing an existing server mutates its definition — level 1.
		if def := h.custom.DefForServer(r.Context(), body.ID); def != nil && !customconn.CanMutate(def, user) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
	}
	_, key, instanceID, err := h.custom.SaveServer(r.Context(), &body.Form, body.TestedOK, body.ID, userID(user))
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	// Level 2 ownership: the auto-created first instance belongs to its
	// non-admin creator (admins manage everything anyway).
	if instanceID != "" && user != nil && !user.IsAdmin() {
		h.custom.TagInstanceOwner(r.Context(), instanceID, user.ID)
	}
	// The connector exists the moment the server row does; instances are
	// created on its page via "+ New row".
	redirect := "/manager/connectors/" + key
	if instanceID != "" {
		redirect += "/" + instanceID
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": redirect})
}

// ── OAuth (MCP authorization spec) ───────────────────────────────────

// customOAuthRedirectURI builds the absolute callback URL, preferring
// the configured app_url and falling back to the request host.
func (h *Handler) customOAuthRedirectURI(r *http.Request) string {
	base := strings.TrimRight(h.configs.AppURL(), "/")
	if base == "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/manager/connectors/custom/mcp-servers/oauth/callback"
}

// customMCPOAuthStart begins the popup login for the register/edit
// form: discovery + (when needed) dynamic client registration, then
// hands the authorization URL back to the JS, which opens the popup.
func (h *Handler) customMCPOAuthStart(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	var body struct {
		Form customconn.ServerForm `json:"form"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	authURL, loginID, err := h.custom.StartOAuthLogin(r.Context(), &body.Form, h.customOAuthRedirectURI(r), "")
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"auth_url": authURL, "login_id": loginID})
}

// customMCPOAuthStatus reports an in-flight login's state — the form
// polls it while the popup is out, because COOP-severed popup handles
// make both window.opener and window.closed unreliable.
func (h *Handler) customMCPOAuthStatus(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, true) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": h.custom.OAuthLoginStatus(r.URL.Query().Get("login_id")),
	})
}

// customMCPOAuthCallback finishes the code exchange. Popup logins post
// the login id back to the opener form and close; instance-bound
// logins persist the tokens, re-sync the def (the module may have had
// zero ops while no account existed), and land on the instance page.
func (h *Handler) customMCPOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, false) {
		return
	}
	q := r.URL.Query()
	if errCode := q.Get("error"); errCode != "" {
		desc := q.Get("error_description")
		customOAuthPopupHTML(w, "", errCode+": "+desc)
		return
	}
	res, err := h.custom.CompleteOAuthLogin(r.Context(), q.Get("state"), q.Get("code"), h.customOAuthRedirectURI(r))
	if err != nil {
		customOAuthPopupHTML(w, "", err.Error())
		return
	}
	if res.InstanceID != "" {
		if defID, ok := h.custom.DefIDForKey(res.Key); ok {
			if err := h.custom.Reload(r.Context(), defID); err != nil {
				log.Warn().Err(err).Str("key", res.Key).Msg("re-sync after oauth connect")
			}
		}
		http.Redirect(w, r, "/manager/connectors/"+res.Key+"/"+res.InstanceID, http.StatusFound)
		return
	}
	customOAuthPopupHTML(w, res.LoginID, "")
}

// customOAuthPopupHTML renders the tiny popup-closing page: it posts
// the result to the opener (the register form listens) and closes.
func customOAuthPopupHTML(w http.ResponseWriter, loginID, errMsg string) {
	payload, _ := json.Marshal(map[string]string{
		"type": "wick-mcp-oauth", "login_id": loginID, "error": errMsg,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// BroadcastChannel first: the authorization server's COOP headers
	// sever window.opener on the cross-origin hop, so postMessage alone
	// never reaches the form. The channel is same-origin and immune.
	_, _ = w.Write([]byte(`<!doctype html><html><body style="font-family:sans-serif;padding:2rem">
<p>` + popupMessage(loginID, errMsg) + `</p>
<script>
try { new BroadcastChannel("wick-mcp-oauth").postMessage(` + string(payload) + `); } catch (e) {}
if (window.opener) { try { window.opener.postMessage(` + string(payload) + `, window.location.origin); } catch (e) {} }
setTimeout(function(){ window.close(); }, 800);
</script></body></html>`))
}

func popupMessage(loginID, errMsg string) string {
	if errMsg != "" {
		return "Login failed. You can close this window."
	}
	_ = loginID
	return "Login successful — you can close this window."
}

// customMCPConnectInstance attaches an account to an existing instance
// row: full-page redirect into the authorization URL (no form state to
// lose), callback persists the tokens and returns to the instance.
func (h *Handler) customMCPConnectInstance(w http.ResponseWriter, r *http.Request) {
	if h.customNotReady(w, r, false) {
		return
	}
	ctx := r.Context()
	user := login.GetUser(ctx)
	instanceID := r.FormValue("instance_id")
	row, err := h.connectors.Get(ctx, instanceID)
	if err != nil || !h.canSeeRow(r, user, row.ID) {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	// Level-2 rule: attaching an account configures the row — owner,
	// admin, or rows that allow others to configure.
	if !h.canConfigureRow(user, row) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	defID, ok := h.custom.DefIDForKey(row.Key)
	if !ok {
		http.Error(w, "not a custom connector instance", http.StatusBadRequest)
		return
	}
	def, err := h.custom.Store().GetDef(ctx, defID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	srv, err := h.custom.Store().GetServer(ctx, customconn.ServerIDForDef(def))
	if err != nil {
		http.Error(w, "mcp server row missing", http.StatusNotFound)
		return
	}
	authURL, _, err := h.custom.StartOAuthLoginForServer(srv, h.customOAuthRedirectURI(r), instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

// ── public key ───────────────────────────────────────────────────────

// customPubkeyPEM serves the SSO verification key MCP servers validate
// X-Wick-User JWTs against. Public — registered without auth.
func (h *Handler) customPubkeyPEM(w http.ResponseWriter, r *http.Request) {
	if h.custom == nil {
		http.NotFound(w, r)
		return
	}
	pem, err := h.custom.SSO().PublicKeyPEM()
	if err != nil {
		http.Error(w, "signing key unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(pem))
}
