package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/connectors"
	customconn "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
)

// connectorRoutes wires the /manager/connectors/* surface. Called from
// Handler.Register so all manager routes live under one mux registration.
func (h *Handler) connectorRoutes(mux *http.ServeMux, authMidd *login.Middleware) {
	// One-shot boot migration: copy legacy shared OAuth App credentials into
	// per-instance rows for any connector that has not yet been configured.
	go h.migrateOAuthAppToInstances(context.Background())

	auth := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAuth(next)
	}
	admin := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAdmin(next)
	}

	// Connectors index page → SPA shell (client route "/"). The JSON twin
	// below stays; the SPA reads it.
	mux.Handle("GET /manager/connectors", auth(http.HandlerFunc(h.serveConnectorsShell)))
	mux.Handle("GET /manager/api/connectors", auth(h.apiConnectors))

	// JSON read/write surface for the manager SPA (Phase 2). Mirrors the
	// templ /manager/connectors/* routes below but speaks JSON, reusing the
	// same services + permission gates. The templ routes stay intact for
	// coexistence during the migration.
	mux.Handle("GET /manager/api/connectors/{key}", auth(h.apiConnectorRows))
	mux.Handle("GET /manager/api/connectors/{key}/{id}", auth(h.apiConnectorDetail))
	mux.Handle("POST /manager/api/connectors/{key}/new", auth(h.apiCreateConnectorRow))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/label", auth(h.apiSetConnectorLabel))
	mux.Handle("POST /manager/api/connectors/{key}/reload", auth(h.apiConnectorReload))
	// Connector-TYPE off-switch (admin-only): hide/show the whole connector
	// type from the LLM. Distinct from the per-row {id}/disable below.
	mux.Handle("POST /manager/api/connectors/{key}/type-disable", admin(h.apiSetConnectorTypeDisabled(true)))
	mux.Handle("POST /manager/api/connectors/{key}/type-enable", admin(h.apiSetConnectorTypeDisabled(false)))
	mux.Handle("POST /manager/api/connectors/{key}/resync-tools", auth(h.apiResyncMCPTools))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/configs/{configKey}", auth(h.apiSetConnectorConfig))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/disable", auth(h.apiToggleConnectorDisabled))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/delete", auth(h.apiDeleteConnectorRow))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/health-check", auth(h.runConnectorHealthCheck))
	// Phase 3 — test runner + run history JSON for the SPA. test-meta
	// exposes each op's input schema (previously templ-only); test runs
	// the op (alias of the legacy /test JSON handler); history serves the
	// filtered + paginated audit log.
	mux.Handle("GET /manager/api/connectors/{key}/{id}/test-meta", auth(h.apiConnectorTestMeta))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/test", auth(h.apiTestConnectorOperation))
	mux.Handle("GET /manager/api/connectors/{key}/{id}/history", auth(h.apiConnectorHistory))

	// Phase 7a — JSON twins of the per-row admin controls (rate limit,
	// duplicate, access policy, session config, operation toggles, and
	// account management). Same services + permission gates as the templ
	// routes below; SPA-driven, no page reload.
	mux.Handle("POST /manager/api/connectors/{key}/{id}/rate-limit", auth(h.apiSetConnectorRateLimit))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/duplicate", auth(h.apiDuplicateConnector))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/access-policy", auth(h.apiSetConnectorAccessPolicy))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/session-config", auth(h.apiSetConnectorSessionConfig))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/operations/bulk", auth(h.apiBulkToggleOperations))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/operations/{opKey}", auth(h.apiToggleConnectorOperation))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/operations/{opKey}/admin-only", auth(h.apiToggleOperationAdminOnly))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/accounts/{accountID}/disconnect", auth(h.apiDisconnectAccount))
	mux.Handle("POST /manager/api/connectors/{key}/{id}/accounts/{accountID}/ops", auth(h.apiSetAccountDisabledOps))

	// Connector page routes → SPA shell. The SPA's client router resolves
	// the rest of the path. Every POST/mutation route below stays live —
	// they are the write surface and remain callable independent of which
	// UI drives them.
	mux.Handle("GET /manager/connectors/{key}", auth(http.HandlerFunc(h.serveConnectorsShell)))
	mux.Handle("POST /manager/connectors/{key}/new", auth(h.createConnectorRow))
	mux.Handle("GET /manager/connectors/{key}/{id}", auth(http.HandlerFunc(h.serveConnectorsShell)))
	mux.Handle("POST /manager/connectors/{key}/{id}/label", auth(h.setConnectorLabel))
	mux.Handle("POST /manager/connectors/{key}/{id}/configs/{configKey}", auth(h.setConnectorConfig))
	mux.Handle("POST /manager/connectors/{key}/{id}/disable", auth(h.toggleConnectorDisabled))
	mux.Handle("POST /manager/connectors/{key}/{id}/rate-limit", auth(h.setConnectorRateLimit))
	mux.Handle("POST /manager/connectors/{key}/{id}/duplicate", auth(h.duplicateConnector))
	mux.Handle("POST /manager/connectors/{key}/{id}/delete", auth(h.deleteConnector))
	mux.Handle("POST /manager/connectors/{key}/{id}/access-policy", auth(h.setConnectorAccessPolicy))
	mux.Handle("POST /manager/connectors/{key}/{id}/session-config", auth(h.setConnectorSessionConfig))
	// Account-ops controls live in the SPA row detail; the page route
	// renders the shell. The account mutation routes below stay live.
	mux.Handle("GET /manager/connectors/{key}/{id}/accounts/{accountID}", auth(http.HandlerFunc(h.serveConnectorsShell)))
	mux.Handle("POST /manager/connectors/{key}/{id}/accounts/{accountID}/disconnect", auth(h.disconnectAccount))
	mux.Handle("POST /manager/connectors/{key}/{id}/accounts/{accountID}/ops", auth(h.setAccountDisabledOps))
	mux.Handle("POST /manager/connectors/{key}/{id}/accounts/{accountID}/ops/{opKey}", auth(h.toggleAccountOp))
	mux.Handle("POST /manager/connectors/{key}/{id}/operations/bulk", auth(h.bulkToggleOperations))
	mux.Handle("POST /manager/connectors/{key}/{id}/operations/{opKey}", auth(h.toggleConnectorOperation))
	mux.Handle("POST /manager/connectors/{key}/{id}/operations/{opKey}/admin-only", auth(h.toggleOperationAdminOnly))
	mux.Handle("POST /manager/connectors/{key}/{id}/health-check", auth(h.runConnectorHealthCheck))
	mux.Handle("GET /manager/connectors/{key}/{id}/test", auth(http.HandlerFunc(h.serveConnectorsShell)))
	mux.Handle("POST /manager/connectors/{key}/{id}/test", auth(h.testConnectorOperation))
	mux.Handle("GET /manager/connectors/{key}/{id}/history", auth(http.HandlerFunc(h.serveConnectorsShell)))
}

// ── Connector index/list/detail pages ────────────────────────────────
//
// The templ index/list/detail/test/history/account-ops pages were removed
// in the SPA cutover. Their GET routes 302 to the SPA, which reads the JSON
// twins (apiConnectors / apiConnectorRows / apiConnectorDetail / test-meta /
// history). connectorCategory + hasDefaultTag below are still used by
// apiConnectors to bucket connectors by category for the SPA index.

// connectorCategory picks the display category for a connector from its
// DefaultTags: the first group tag that is neither "Connector" (the
// umbrella every connector carries) nor "System". Returns the category
// name, its sort order, and its description. Falls back to "System" for
// system connectors with no other category, else "Other".
func connectorCategory(list []tool.DefaultTag, system bool) (name string, sortOrder int, desc string) {
	for _, t := range list {
		// Skip the role tag, the System tag, and per-def custom access tags
		// ("custom:<key>"): none of those are categories. A custom connector
		// with no real category tag falls through to "Other" below — it must
		// never show up as its own "custom:beo_echo" category chip.
		if t.Name == tags.Connector.Name || t.Name == tags.System.Name || customconn.IsCustomTag(t.Name) {
			continue
		}
		return t.Name, t.SortOrder, t.Description
	}
	if system {
		return tags.System.Name, tags.System.SortOrder, tags.System.Description
	}
	return "Other", 1<<31 - 1, ""
}

// hasDefaultTag reports whether a connector's DefaultTags include a tag
// with the given name.
func hasDefaultTag(list []tool.DefaultTag, name string) bool {
	for _, t := range list {
		if t.Name == name {
			return true
		}
	}
	return false
}

// resolveRowTags returns, per connector row ID, the user-facing access tag
// names linked to it (owner:<id> markers excluded) and whether the row
// carries an owner tag (private set). A row is "private" when it has an
// owner tag: only the owner + admins see it until an admin adds a sharing
// tag. The list view renders: real tags if any; else 🔒 Private if owned;
// else Everyone.
func (h *Handler) resolveRowTags(ctx context.Context, rows []entity.Connector) (map[string][]string, map[string]bool) {
	out := map[string][]string{}
	private := map[string]bool{}
	if h.tags == nil || len(rows) == 0 {
		return out, private
	}
	paths := make([]string, len(rows))
	pathToID := make(map[string]string, len(rows))
	for i, row := range rows {
		p := "/connectors/" + row.ID
		paths[i] = p
		pathToID[p] = row.ID
	}
	idsByPath, err := h.tags.ToolTagIDs(ctx, paths)
	if err != nil {
		return out, private
	}
	uniq := map[string]struct{}{}
	for _, ids := range idsByPath {
		for _, id := range ids {
			uniq[id] = struct{}{}
		}
	}
	if len(uniq) == 0 {
		return out, private
	}
	all := make([]string, 0, len(uniq))
	for id := range uniq {
		all = append(all, id)
	}
	tagRows, err := h.tags.TagsByIDs(ctx, all)
	if err != nil {
		return out, private
	}
	nameByID := make(map[string]string, len(tagRows))
	for _, t := range tagRows {
		nameByID[t.ID] = t.Name
	}
	for path, ids := range idsByPath {
		rowID := pathToID[path]
		for _, id := range ids {
			if n, ok := nameByID[id]; ok {
				// "owner:<id>" tags are the internal per-row access marker
				// seeded on non-admin create — not a user-facing access tag.
				// A row carrying one is private (owner + admins only) until an
				// admin adds a real sharing tag; track that, but don't render
				// the raw "owner:<uuid>" as a chip.
				if strings.HasPrefix(n, "owner:") {
					private[rowID] = true
					continue
				}
				out[rowID] = append(out[rowID], n)
			}
		}
	}
	return out, private
}

// resolveRunUsers returns id→display map for every distinct UserID
// appearing in the run set. Missing/blank IDs map to "system".
func (h *Handler) resolveRunUsers(ctx context.Context, runs []entity.ConnectorRun) map[string]string {
	out := map[string]string{}
	if h.users == nil {
		return out
	}
	seen := map[string]struct{}{}
	for _, r := range runs {
		if r.UserID == "" {
			continue
		}
		seen[r.UserID] = struct{}{}
	}
	for id := range seen {
		u, err := h.users.GetUserByID(ctx, id)
		if err != nil || u == nil {
			out[id] = id
			continue
		}
		label := u.Name
		if label == "" {
			label = u.Email
		}
		out[id] = label
	}
	return out
}

// ── Row CRUD ─────────────────────────────────────────────────────────

func (h *Handler) createConnectorRow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")

	mod, ok := h.connectors.Module(key)
	if !ok {
		http.Error(w, "unknown connector", http.StatusNotFound)
		return
	}
	row, err := h.connectors.Create(ctx, key, mod.Meta.Name+" (new)", map[string]string{}, userID(user))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Assign ownership tag to the creating user (non-admin creates only).
	if h.tags != nil && user != nil && !user.IsAdmin() {
		if err := h.tags.CreateOwnerTag(ctx, row.ID, user.ID); err != nil {
			log.Warn().Err(err).Str("row_id", row.ID).Msg("manager: create owner tag failed")
		}
	}
	// Custom defs link their access tags per instance — run it now so the
	// fresh row is governed immediately instead of on the next boot.
	if h.custom != nil {
		h.custom.EnsureTagsForKey(ctx, key)
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+row.ID, http.StatusFound)
}

func (h *Handler) setConnectorLabel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !h.canConfigureRow(user, row) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		http.Error(w, "label is required", http.StatusBadRequest)
		return
	}
	stored := h.connectors.LoadConfigs(*row)
	if err := h.connectors.Update(ctx, row.ID, label, stored, row.Disabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+row.ID, http.StatusFound)
}

func (h *Handler) setConnectorConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")
	configKey := r.PathValue("configKey")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !h.canConfigureRow(user, row) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	for _, cfg := range h.connectors.RowConfigs(*row) {
		if cfg.Key == configKey && cfg.IsSecret {
			if !h.canConfigureSecretField(user, row) {
				http.Error(w, "forbidden: secret fields require admin or connector owner", http.StatusForbidden)
				return
			}
			break
		}
	}
	stored := h.connectors.LoadConfigs(*row)
	stored[configKey] = r.FormValue("value")
	if err := h.connectors.Update(ctx, row.ID, row.Label, stored, row.Disabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+row.ID, http.StatusFound)
}

func (h *Handler) toggleConnectorDisabled(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := h.connectors.SetDisabled(ctx, row.ID, !row.Disabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+row.ID, http.StatusFound)
}

func (h *Handler) setConnectorRateLimit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !h.canConfigureRow(user, row) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	rpm, _ := strconv.Atoi(r.FormValue("rpm"))
	if rpm < 0 {
		rpm = 0
	}
	if err := h.connectors.SetRateLimit(ctx, row.ID, rpm); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+row.ID, http.StatusFound)
}

func (h *Handler) toggleAccountOp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")
	accountID := r.PathValue("accountID")
	opKey := r.PathValue("opKey")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	acc, err := h.connectors.GetAccount(ctx, accountID)
	if err != nil || acc.ConnectorID != row.ID {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	// Parse current disabled ops, toggle the target op.
	disabled := connectors.AccountDisabledOps(acc)
	if disabled == nil {
		disabled = map[string]bool{}
	}
	enabled := boolParam(r, "enabled")
	if enabled {
		delete(disabled, opKey)
	} else {
		disabled[opKey] = true
	}
	keys := make([]string, 0, len(disabled))
	for k := range disabled {
		keys = append(keys, k)
	}
	if err := h.connectors.SetAccountDisabledOps(ctx, accountID, keys); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+id+"/accounts/"+accountID, http.StatusSeeOther)
}

func (h *Handler) setAccountDisabledOps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")
	accountID := r.PathValue("accountID")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	acc, err := h.connectors.GetAccount(ctx, accountID)
	if err != nil || acc.ConnectorID != row.ID {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}
	if !h.canConfigureRow(user, row) && (user == nil || acc.WickUserID != user.ID) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// Collect checked op keys from form (checkbox name="ops", value=opKey).
	ops := r.Form["ops"]
	if ops == nil {
		ops = []string{}
	}
	if err := h.connectors.SetAccountDisabledOps(ctx, accountID, ops); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+key, http.StatusSeeOther)
}

func (h *Handler) disconnectAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")
	accountID := r.PathValue("accountID")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	acc, err := h.connectors.GetAccount(ctx, accountID)
	if err != nil || acc.ConnectorID != row.ID {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}
	// Only admin or owner can disconnect.
	if !h.canConfigureRow(user, row) && (user == nil || acc.WickUserID != user.ID) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	if err := h.connectors.DeleteAccount(ctx, accountID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+key, http.StatusSeeOther)
}

func (h *Handler) setConnectorAccessPolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Only admins can change the access policy.
	if user == nil || !user.IsAdmin() {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	allowConfigure := boolParam(r, "allow_others_configure")
	allowSSO := boolParam(r, "allow_others_connect_sso")
	enableSSO := boolParam(r, "enable_sso")
	multiAccount := boolParam(r, "multi_account")
	if err := h.connectors.SetAccessPolicy(ctx, row.ID, allowConfigure, allowSSO, enableSSO, multiAccount); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+row.ID, http.StatusFound)
}

// setConnectorSessionConfig flips the per-instance "allow per-session
// config override" opt-in. Admin-only, and only honored when the
// connector's module is session-config capable.
func (h *Handler) setConnectorSessionConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if user == nil || !user.IsAdmin() {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	if !h.connectors.SessionConfigCapable(row.Key) {
		http.Error(w, "this connector does not support per-session config override", http.StatusBadRequest)
		return
	}
	if err := h.connectors.SetSessionConfigAllowed(ctx, row.ID, boolParam(r, "allow_session_config")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+row.ID, http.StatusFound)
}

func (h *Handler) duplicateConnector(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	dup, err := h.connectors.Duplicate(ctx, row.ID, userID(user))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// User who duplicates becomes owner of the new row.
	if h.tags != nil && user != nil && !user.IsAdmin() {
		if err := h.tags.CreateOwnerTag(ctx, dup.ID, user.ID); err != nil {
			log.Warn().Err(err).Str("row_id", dup.ID).Msg("manager: create owner tag on duplicate failed")
		}
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+dup.ID, http.StatusFound)
}

func (h *Handler) deleteConnector(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !h.canConfigureRow(user, row) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := h.connectors.Delete(ctx, row.ID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Clean up owner tag + all its UserTag/ToolTag associations.
	if h.tags != nil {
		if err := h.tags.DeleteOwnerTag(ctx, row.ID); err != nil {
			log.Warn().Err(err).Str("row_id", row.ID).Msg("manager: delete owner tag failed")
		}
	}
	http.Redirect(w, r, "/manager/connectors/"+key, http.StatusFound)
}

// ── Operation toggles ────────────────────────────────────────────────

func (h *Handler) bulkToggleOperations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	mod, ok := h.connectors.Module(key)
	if !ok {
		http.Error(w, "unknown connector", http.StatusNotFound)
		return
	}
	enabled := boolParam(r, "enabled")
	for _, op := range mod.AllOps() {
		if err := h.connectors.SetOperationEnabled(ctx, row.ID, op.Key, enabled); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+row.ID, http.StatusFound)
}

func (h *Handler) toggleConnectorOperation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")
	opKey := r.PathValue("opKey")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !h.canConfigureRow(user, row) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	enabled := boolParam(r, "enabled")
	if err := h.connectors.SetOperationEnabled(ctx, row.ID, opKey, enabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Admin explicitly enabling an op clears any system_disabled flag —
	// treating it as an intentional override of the health-check warning.
	if enabled {
		_ = h.connectors.ClearSystemDisabled(ctx, row.ID, opKey)
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+row.ID, http.StatusFound)
}

// runConnectorHealthCheck invokes the connector module's HealthCheck
// hook for one row and lets the service reconcile system_disabled flags
// against the report. Returns a JSON summary so the row page can update
// the banner + op rows inline without a full reload. The button in the
// template fetches this endpoint with `Accept: application/json`.
func (h *Handler) runConnectorHealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	result, err := h.connectors.RunHealthCheck(ctx, row.ID)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	states, _ := h.connectors.OperationStatesFull(ctx, row.ID, row.Key)
	opsOut := make(map[string]map[string]any, len(states))
	for k, st := range states {
		opsOut[k] = map[string]any{
			"enabled":         st.Enabled,
			"system_disabled": st.SystemDisabled,
			"reason":          st.SystemDisabledReason,
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":            true,
		"newly_locked":  result.NewlyLocked,
		"newly_cleared": result.NewlyCleared,
		"ops":           opsOut,
	})
}

func (h *Handler) toggleOperationAdminOnly(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")
	opKey := r.PathValue("opKey")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if user == nil || !user.IsAdmin() {
		http.Error(w, "forbidden: admin only", http.StatusForbidden)
		return
	}
	adminOnly := boolParam(r, "admin_only")
	if err := h.connectors.SetOperationAdminOnly(ctx, row.ID, opKey, adminOnly); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+key+"/"+row.ID, http.StatusFound)
}

// ── Test panel ───────────────────────────────────────────────────────

// testConnectorOperation accepts a JSON body {operation, input:{...}} and
// runs Service.Execute with Source=test. Returns the run summary as JSON
// so the test panel can render request/response without a page reload.
func (h *Handler) testConnectorOperation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "connector not found"})
		return
	}

	var body struct {
		Operation string            `json:"operation"`
		Input     map[string]string `json:"input"`
		AccountID string            `json:"account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if body.Input == nil {
		body.Input = map[string]string{}
	}

	res, execErr := h.connectors.Execute(ctx, connectors.ExecuteParams{
		ConnectorID:  row.ID,
		OperationKey: body.Operation,
		Input:        body.Input,
		Source:       entity.ConnectorRunSourceTest,
		UserID:       userID(user),
		IPAddress:    clientIP(r),
		UserAgent:    r.UserAgent(),
		AccountID:    body.AccountID,
	})

	out := map[string]any{
		"operation": body.Operation,
		"input":     body.Input,
	}
	if res != nil {
		out["run_id"] = res.RunID
		out["status"] = string(res.Status)
		out["latency_ms"] = res.LatencyMs
		out["response"] = json.RawMessage(orEmptyJSON(res.ResponseJSON))
		if res.ErrorMessage != "" {
			out["error"] = res.ErrorMessage
		}
	}
	if execErr != nil && (res == nil || res.ErrorMessage == "") {
		out["error"] = execErr.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// ── Helpers ──────────────────────────────────────────────────────────

// canConfigureRow reports whether the user may edit credentials/settings on
// the given connector row.
// Allow order: admin → owner tag → AllowOthersConfigure.
func (h *Handler) canConfigureRow(user *entity.User, row *entity.Connector) bool {
	if user == nil {
		return false
	}
	if user.IsAdmin() {
		return true
	}
	if h.tags != nil {
		owns, _ := h.tags.UserOwnsConnector(context.Background(), user.ID, row.ID)
		if owns {
			return true
		}
	}
	return row.AllowOthersConfigure
}

// canConfigureSecretField reports whether the user may write a secret config
// field on the given connector row.
// Allow order: admin → owner tag. AllowOthersConfigure is intentionally NOT
// sufficient — any user granted AllowOthersConfigure could otherwise overwrite
// credentials they should not see.
func (h *Handler) canConfigureSecretField(user *entity.User, row *entity.Connector) bool {
	if user == nil {
		return false
	}
	if user.IsAdmin() {
		return true
	}
	if h.tags != nil {
		owns, _ := h.tags.UserOwnsConnector(context.Background(), user.ID, row.ID)
		if owns {
			return true
		}
	}
	return false
}

// canConnectSSO reports whether the user may initiate an OAuth connect on the
// given connector row.
// Allow order: admin → owner tag → AllowOthersConnectSSO.
func (h *Handler) canConnectSSO(user *entity.User, row *entity.Connector) bool {
	if user == nil {
		return false
	}
	if user.IsAdmin() {
		return true
	}
	if h.tags != nil {
		owns, _ := h.tags.UserOwnsConnector(context.Background(), user.ID, row.ID)
		if owns {
			return true
		}
	}
	return row.AllowOthersConnectSSO
}

// userFilterTagIDs resolves the caller's filter tag IDs live from the DB
// rather than the session cookie. The cookie is a login-time snapshot, so a
// tag created mid-session (owner tag on create/duplicate, or one an admin
// just granted) wouldn't appear until re-login. Querying per request keeps
// visibility correct with no cookie re-issue. Falls back to the cookie when
// the login service isn't wired (tests) or the user is nil.
func (h *Handler) userFilterTagIDs(ctx context.Context, user *entity.User) []string {
	if h.users != nil && user != nil {
		return h.users.GetUserFilterTagIDs(ctx, user.ID)
	}
	return login.GetUserTagIDs(ctx)
}

func (h *Handler) visibleRowsForKey(r *http.Request, user *entity.User, key string) ([]entity.Connector, error) {
	ctx := r.Context()
	rows, err := h.connectors.ListForManager(ctx, h.userFilterTagIDs(ctx, user), user != nil && user.IsAdmin())
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, r := range rows {
		if r.Key == key {
			out = append(out, r)
		}
	}
	return out, nil
}

// canSeeRow gates per-row manager actions. Disabled rows are still
// manageable so the caller can re-enable them — execution surfaces
// (MCP, panel test) gate disabled separately inside Service.Execute.
func (h *Handler) canSeeRow(r *http.Request, user *entity.User, connectorID string) bool {
	ctx := r.Context()
	isAdmin := user != nil && user.IsAdmin()
	// Live tag IDs (not the cookie snapshot) so a just-created owner tag is
	// honored without a re-login — same source visibleRowsForKey uses.
	ok, err := h.connectors.IsManageableBy(ctx, connectorID, h.userFilterTagIDs(ctx, user), isAdmin)
	return err == nil && ok
}

func userID(u *entity.User) string {
	if u == nil {
		return ""
	}
	return u.ID
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func orEmptyJSON(s string) string {
	if s == "" {
		return "null"
	}
	return s
}

// _ keeps the connector import alive when this file is the only consumer.
var _ connector.Module
