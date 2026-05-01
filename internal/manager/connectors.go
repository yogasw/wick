package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager/view"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/pkg/connector"
)

// connectorRoutes wires the /manager/connectors/* surface. Called from
// Handler.Register so all manager routes live under one mux registration.
func (h *Handler) connectorRoutes(mux *http.ServeMux, authMidd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAuth(next)
	}

	mux.Handle("GET /manager/connectors/{key}", auth(h.connectorListPage))
	mux.Handle("POST /manager/connectors/{key}/new", auth(h.createConnectorRow))
	mux.Handle("GET /manager/connectors/{key}/{id}", auth(h.connectorDetailPage))
	mux.Handle("POST /manager/connectors/{key}/{id}/label", auth(h.setConnectorLabel))
	mux.Handle("POST /manager/connectors/{key}/{id}/configs/{configKey}", auth(h.setConnectorConfig))
	mux.Handle("POST /manager/connectors/{key}/{id}/disable", auth(h.toggleConnectorDisabled))
	mux.Handle("POST /manager/connectors/{key}/{id}/duplicate", auth(h.duplicateConnector))
	mux.Handle("POST /manager/connectors/{key}/{id}/delete", auth(h.deleteConnector))
	mux.Handle("POST /manager/connectors/{key}/{id}/operations/{opKey}", auth(h.toggleConnectorOperation))
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
	view.ConnectorListPage(mod, rows, tagsByRow, user).Render(ctx, w)
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

	configs := buildRowConfigs(mod.Configs, decodeConfigs(row.Configs))
	opStates, _ := h.connectors.OperationStates(ctx, row.ID, row.Key)
	editKey := r.URL.Query().Get("edit")

	view.ConnectorDetailPage(mod, row, configs, opStates, editKey, user).Render(ctx, w)
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
	view.ConnectorTestPage(mod, row, activeOp, user).Render(ctx, w)
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
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		http.Error(w, "label is required", http.StatusBadRequest)
		return
	}
	stored := decodeConfigs(row.Configs)
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
	stored := decodeConfigs(row.Configs)
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
	if err := h.connectors.Delete(ctx, row.ID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/manager/connectors/"+key, http.StatusFound)
}

// ── Operation toggles ────────────────────────────────────────────────

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
	enabled := boolParam(r, "enabled")
	if err := h.connectors.SetOperationEnabled(ctx, row.ID, opKey, enabled); err != nil {
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

// decodeConfigs unmarshals the row's stored JSON configs blob. Empty or
// malformed blobs decode to an empty map so callers can always write.
func decodeConfigs(raw string) map[string]string {
	out := map[string]string{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = map[string]string{}
	}
	return out
}

// buildRowConfigs overlays the row's stored values onto the module's
// declared config schema, producing the rows the existing ConfigsTable
// expects.
func buildRowConfigs(specs []entity.Config, stored map[string]string) []entity.Config {
	out := make([]entity.Config, len(specs))
	for i, s := range specs {
		s.Value = stored[s.Key]
		out[i] = s
	}
	return out
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
