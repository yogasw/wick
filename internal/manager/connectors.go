package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager/view"
	"github.com/yogasw/wick/internal/pkg/ui"
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

	mux.Handle("GET /manager/connectors", auth(h.connectorsIndexPage))
	mux.Handle("GET /manager/api/connectors", auth(h.apiConnectors))
	mux.Handle("GET /manager/connectors/{key}", auth(h.connectorListPage))
	mux.Handle("POST /manager/connectors/{key}/new", auth(h.createConnectorRow))
	mux.Handle("GET /manager/connectors/{key}/{id}", auth(h.connectorDetailPage))
	mux.Handle("POST /manager/connectors/{key}/{id}/label", auth(h.setConnectorLabel))
	mux.Handle("POST /manager/connectors/{key}/{id}/configs/{configKey}", auth(h.setConnectorConfig))
	mux.Handle("POST /manager/connectors/{key}/{id}/disable", auth(h.toggleConnectorDisabled))
	mux.Handle("POST /manager/connectors/{key}/{id}/rate-limit", auth(h.setConnectorRateLimit))
	mux.Handle("POST /manager/connectors/{key}/{id}/duplicate", auth(h.duplicateConnector))
	mux.Handle("POST /manager/connectors/{key}/{id}/delete", auth(h.deleteConnector))
	mux.Handle("POST /manager/connectors/{key}/{id}/access-policy", auth(h.setConnectorAccessPolicy))
	mux.Handle("POST /manager/connectors/{key}/{id}/session-config", auth(h.setConnectorSessionConfig))
	mux.Handle("GET /manager/connectors/{key}/{id}/accounts/{accountID}", auth(h.accountOpsPage))
	mux.Handle("POST /manager/connectors/{key}/{id}/accounts/{accountID}/disconnect", auth(h.disconnectAccount))
	mux.Handle("POST /manager/connectors/{key}/{id}/accounts/{accountID}/ops", auth(h.setAccountDisabledOps))
	mux.Handle("POST /manager/connectors/{key}/{id}/accounts/{accountID}/ops/{opKey}", auth(h.toggleAccountOp))
	mux.Handle("POST /manager/connectors/{key}/{id}/operations/bulk", auth(h.bulkToggleOperations))
	mux.Handle("POST /manager/connectors/{key}/{id}/operations/{opKey}", auth(h.toggleConnectorOperation))
	mux.Handle("POST /manager/connectors/{key}/{id}/operations/{opKey}/admin-only", auth(h.toggleOperationAdminOnly))
	mux.Handle("POST /manager/connectors/{key}/{id}/health-check", auth(h.runConnectorHealthCheck))
	mux.Handle("GET /manager/connectors/{key}/{id}/test", auth(h.connectorTestPage))
	mux.Handle("POST /manager/connectors/{key}/{id}/test", auth(h.testConnectorOperation))
	mux.Handle("GET /manager/connectors/{key}/{id}/history", auth(h.connectorHistoryPage))
}

// ── List page ────────────────────────────────────────────────────────

func (h *Handler) connectorListPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")

	mod, ok := h.connectors.Module(key)
	if !ok {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}

	rows, err := h.visibleRowsForKey(r, user, key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tagsByRow := h.resolveRowTags(ctx, rows)

	// Load connected accounts per row for the sub-account list.
	accountsByRow := make(map[string][]entity.ConnectorAccount, len(rows))
	for _, row := range rows {
		accs, err := h.connectors.ListAccounts(ctx, row.ID)
		if err == nil {
			accountsByRow[row.ID] = accs
		}
	}

	// Compute oauthURL per row for the Connect button on the list page.
	oauthURLByRow := make(map[string]string, len(rows))
	if mod.OAuth != nil {
		for _, row := range rows {
			if !row.EnableSSO {
				continue
			}
			cfgs := h.connectors.LoadConfigs(row)
			if strings.TrimSpace(cfgs["client_id"]) != "" {
				oauthURLByRow[row.ID] = "/manager/connectors/" + key + "/oauth/start?connector_id=" + row.ID
			}
		}
	}

	oauthSuccess := r.URL.Query().Get("oauth") == "success"
	oauthUser := r.URL.Query().Get("user")

	// Definition-level controls render for whoever may mutate the def
	// (admin ∨ creator) — customDefInfo gates internally.
	customInfo := h.customDefInfo(ctx, key, user)

	view.ConnectorListPage(mod, rows, tagsByRow, accountsByRow, oauthURLByRow, oauthSuccess, oauthUser, customInfo, user).Render(ctx, w)
}

// ── Index page ───────────────────────────────────────────────────────

// connectorsIndexPage lists every registered connector definition,
// grouped by category tag, with search + filter chips. It is the single
// home-page launcher's destination: home shows one "Connectors" tile
// instead of one tile per definition, so this page is where the full set
// is browsed. Non-admins only see definitions they can manage at least
// one row of; System connectors are admin-only.
func (h *Handler) connectorsIndexPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	isAdmin := user != nil && user.IsAdmin()

	rows, err := h.connectors.ListForManager(ctx, login.GetUserTagIDs(ctx), isAdmin)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Count instance states per connector, scoped to the rows the caller
	// can manage (ListForManager already applies tag access; admins see
	// every row). Three states, because an enabled instance is not
	// necessarily usable — its required config may still be unfilled:
	//   active     = enabled AND config complete (ready to use)
	//   needsSetup = enabled BUT required config missing (not ready)
	//   disabled   = row-level off-switch on
	type instanceCount struct{ active, needsSetup, disabled int }
	countByKey := make(map[string]instanceCount, len(rows))
	for _, row := range rows {
		c := countByKey[row.Key]
		switch {
		case row.Disabled:
			c.disabled++
		case h.connectors.Status(row) == "needs_setup":
			c.needsSetup++
		default:
			c.active++
		}
		countByKey[row.Key] = c
	}

	mods := h.connectors.Modules()

	// Bucket connectors by their category tag (the first group tag that
	// is neither the "Connector" umbrella nor "System"). Track each
	// category's sort order so groups render in catalog order.
	type bucket struct {
		sort  int
		desc  string
		cards []view.ConnectorIndexCard
	}
	buckets := make(map[string]*bucket)
	for _, m := range mods {
		system := hasDefaultTag(m.Meta.DefaultTags, tags.System.Name)
		if system && !isAdmin {
			continue
		}
		cnt := countByKey[m.Meta.Key]
		if !isAdmin && cnt.active+cnt.needsSetup+cnt.disabled == 0 {
			continue
		}
		cat, catSort, catDesc := connectorCategory(m.Meta.DefaultTags, system)
		b := buckets[cat]
		if b == nil {
			b = &bucket{sort: catSort, desc: catDesc}
			buckets[cat] = b
		}
		card := view.ConnectorIndexCard{
			Key:             m.Meta.Key,
			Name:            m.Meta.Name,
			Description:     m.Meta.Description,
			Icon:            m.Meta.Icon,
			Category:        cat,
			OpCount:         len(m.Operations),
			ActiveCount:     cnt.active,
			NeedsSetupCount: cnt.needsSetup,
			DisabledCount:   cnt.disabled,
			System:          system,
		}
		if info := h.customDefInfo(ctx, m.Meta.Key, user); info != nil {
			card.Custom = true
			card.CustomSource = info.SourceLabel
			card.NeedsReload = info.Dirty
		}
		b.cards = append(b.cards, card)
	}

	groups := make([]view.ConnectorIndexGroup, 0, len(buckets))
	for name, b := range buckets {
		sort.Slice(b.cards, func(i, j int) bool { return b.cards[i].Name < b.cards[j].Name })
		groups = append(groups, view.ConnectorIndexGroup{Name: name, Description: b.desc, Cards: b.cards})
	}
	sort.Slice(groups, func(i, j int) bool {
		si, sj := buckets[groups[i].Name].sort, buckets[groups[j].Name].sort
		if si != sj {
			return si < sj
		}
		return groups[i].Name < groups[j].Name
	})

	// + New connector is open to every approved user (ownership level
	// 1: anyone creates, only admin/creator mutates).
	view.ConnectorsIndexPage(groups, h.custom != nil, user).Render(ctx, w)
}

// connectorCategory picks the display category for a connector from its
// DefaultTags: the first group tag that is neither "Connector" (the
// umbrella every connector carries) nor "System". Returns the category
// name, its sort order, and its description. Falls back to "System" for
// system connectors with no other category, else "Other".
func connectorCategory(list []tool.DefaultTag, system bool) (name string, sortOrder int, desc string) {
	for _, t := range list {
		if t.Name == tags.Connector.Name || t.Name == tags.System.Name {
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

// resolveRowTags returns a map from connector row ID to the tag names
// linked to it. Used by the list view to render access chips.
func (h *Handler) resolveRowTags(ctx context.Context, rows []entity.Connector) map[string][]string {
	out := map[string][]string{}
	if h.tags == nil || len(rows) == 0 {
		return out
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
		return out
	}
	uniq := map[string]struct{}{}
	for _, ids := range idsByPath {
		for _, id := range ids {
			uniq[id] = struct{}{}
		}
	}
	if len(uniq) == 0 {
		return out
	}
	all := make([]string, 0, len(uniq))
	for id := range uniq {
		all = append(all, id)
	}
	tagRows, err := h.tags.TagsByIDs(ctx, all)
	if err != nil {
		return out
	}
	nameByID := make(map[string]string, len(tagRows))
	for _, t := range tagRows {
		nameByID[t.ID] = t.Name
	}
	for path, ids := range idsByPath {
		rowID := pathToID[path]
		for _, id := range ids {
			if n, ok := nameByID[id]; ok {
				out[rowID] = append(out[rowID], n)
			}
		}
	}
	return out
}

// ── Detail page ──────────────────────────────────────────────────────

func (h *Handler) connectorDetailPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	mod, ok := h.connectors.Module(key)
	if !ok {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	if !h.canSeeRow(r, user, row.ID) {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}

	configs := h.connectors.RowConfigs(*row)
	customInfo := h.customDefInfo(ctx, key, user)
	// Hidden configs (machine-managed values like OAuth tokens) are
	// seeded and readable at runtime but never hand-edited — skip them.
	// The connected-account marker surfaces in the header instead.
	visible := configs[:0]
	for _, cfg := range configs {
		if customInfo != nil {
			switch cfg.Key {
			case "oauth_account":
				// Legacy labels were 'Connected <ts>' before identity
				// resolution existed — not an identity, hide the chip.
				if !strings.HasPrefix(cfg.Value, "Connected ") {
					customInfo.OAuthAccount = cfg.Value
				}
			case "oauth_access_token":
				customInfo.OAuthConnected = cfg.Value != ""
			}
		}
		if !cfg.Hidden {
			visible = append(visible, cfg)
		}
	}
	configs = visible
	opStates, _ := h.connectors.OperationStatesFull(ctx, row.ID, row.Key)
	editKey := r.URL.Query().Get("edit")

	isAdmin := user != nil && user.IsAdmin()
	view.ConnectorDetailPage(mod, row, configs, opStates, editKey, user, view.HealthBanner{}, isAdmin, customInfo).Render(ctx, w)
}

// connectorTestPage renders the standalone Postman-style test surface
// for one connector row. The active operation comes from `?op=`; the
// page itself swaps forms client-side and updates the URL so picking a
// different operation never costs a round trip.
func (h *Handler) connectorTestPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	mod, ok := h.connectors.Module(key)
	if !ok {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	if !h.canSeeRow(r, user, row.ID) {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}

	activeOp := r.URL.Query().Get("op")
	if activeOp == "" && len(mod.Operations) > 0 {
		activeOp = mod.Operations[0].Key
	}

	// `?prefill=<runID>` populates the active op's input fields with the
	// payload from a prior run so the user can review/edit before
	// re-running. Silent fall-through on lookup failure — prefill is a
	// convenience, not a hard dependency.
	var prefill map[string]string
	if runID := r.URL.Query().Get("prefill"); runID != "" {
		if run, err := h.connectors.GetRun(ctx, runID); err == nil && run != nil && run.ConnectorID == row.ID {
			if activeOp == "" {
				activeOp = run.OperationKey
			}
			if run.RequestJSON != "" {
				_ = json.Unmarshal([]byte(run.RequestJSON), &prefill)
			}
		}
	}

	accounts, _ := h.connectors.ListAccounts(ctx, row.ID)
	view.ConnectorTestPage(mod, row, activeOp, prefill, accounts, user).Render(ctx, w)
}

// connectorHistoryPage renders the standalone runs audit surface with
// filter chips. Filters are URL-driven so links can be shared.
func (h *Handler) connectorHistoryPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	mod, ok := h.connectors.Module(key)
	if !ok {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	if !h.canSeeRow(r, user, row.ID) {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}

	filter := connectors.RunFilter{
		OperationKey: r.URL.Query().Get("op"),
		Source:       r.URL.Query().Get("source"),
		Status:       r.URL.Query().Get("status"),
		UserID:       r.URL.Query().Get("user"),
	}

	const pageSize = 10
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	total, _ := h.connectors.CountRunsFiltered(ctx, row.ID, filter)
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	runs, _ := h.connectors.ListRunsFiltered(ctx, row.ID, filter, pageSize, (page-1)*pageSize)
	usersByID := h.resolveRunUsers(ctx, runs)

	view.ConnectorHistoryPage(mod, row, runs, usersByID, filter, page, totalPages, int(total), user).Render(ctx, w)
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

func (h *Handler) accountOpsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")
	accountID := r.PathValue("accountID")

	mod, ok := h.connectors.Module(key)
	if !ok {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	acc, err := h.connectors.GetAccount(ctx, accountID)
	if err != nil || acc.ConnectorID != row.ID {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	disabled := connectors.AccountDisabledOps(acc)
	opStates := make(map[string]connectors.OpState, len(mod.Operations))
	for _, op := range mod.Operations {
		opStates[op.Key] = connectors.OpState{Enabled: !disabled[op.Key]}
	}
	view.ConnectorAccountOpsPage(mod, row, acc, opStates, user).Render(ctx, w)
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
	for _, op := range mod.Operations {
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

func (h *Handler) visibleRowsForKey(r *http.Request, user *entity.User, key string) ([]entity.Connector, error) {
	ctx := r.Context()
	rows, err := h.connectors.ListForManager(ctx, login.GetUserTagIDs(ctx), user != nil && user.IsAdmin())
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
	ok, err := h.connectors.IsManageableBy(ctx, connectorID, login.GetUserTagIDs(ctx), isAdmin)
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
