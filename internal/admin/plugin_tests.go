package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// Plugin test requests let a connector plugin run from source (go run / dlv,
// `--run-op`, see pkg/plugin/runop.go) use a real instance's config. Three
// gates, all of which must hold at the moment the config is handed out:
//
//  1. the master switch plugin_test_enabled (default off);
//  2. the connector key is listed in plugin_test_connectors;
//  3. an admin approved this one request, and it has not expired or been
//     revoked.
//
// The config is only ever returned to the PAT owner who asked for it, and
// every hand-out bumps FetchCount/LastFetchAt on the request row.

const pluginTestMaxMinutes = 240

func (h *Handler) pluginTestAllowed(key string) error {
	if !strings.EqualFold(strings.TrimSpace(h.configs.Get(configs.KeyPluginTestEnabled)), "true") {
		return errors.New("plugin test requests are disabled on this wick (Admin → Plugins → Development mode)")
	}
	for _, k := range strings.Split(h.configs.Get(configs.KeyPluginTestConnectors), ",") {
		if strings.EqualFold(strings.TrimSpace(k), key) {
			return nil
		}
	}
	return errors.New("connector " + key + " is not in the plugin test allowlist (Admin → Plugins → Development mode)")
}

func pluginTestState(r entity.PluginTestRequest, now time.Time) string {
	switch r.Status {
	case entity.PluginTestApproved:
		if r.Active(now) {
			return "active"
		}
		return "expired"
	default:
		return r.Status
	}
}

// ── admin page ─────────────────────────────────────────────────

func (h *Handler) pluginTestsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var reqs []entity.PluginTestRequest
	if err := h.repo.db.WithContext(ctx).Order("created_at desc").Limit(200).Find(&reqs).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	owners := map[string]*entity.User{}
	rows := make([]view.PluginTestRow, 0, len(reqs))
	for _, q := range reqs {
		u, seen := owners[q.UserID]
		if !seen {
			u, _ = h.repo.GetUser(ctx, q.UserID)
			owners[q.UserID] = u
		}
		row := view.PluginTestRow{Req: q, State: pluginTestState(q, now)}
		if u != nil {
			row.OwnerName, row.OwnerEmail = u.Name, u.Email
		}
		rows = append(rows, row)
	}
	vm := view.PluginTestsVM{
		Enabled:    strings.EqualFold(h.configs.Get(configs.KeyPluginTestEnabled), "true"),
		Connectors: h.configs.Get(configs.KeyPluginTestConnectors),
		Rows:       rows,
	}
	_ = view.PluginTestsPage(vm, login.GetUser(ctx)).Render(ctx, w)
}

func (h *Handler) pluginTestsSettings(w http.ResponseWriter, r *http.Request) {
	enabled := "false"
	if r.FormValue("enabled") == "true" {
		enabled = "true"
	}
	var keys []string
	for _, k := range strings.Split(r.FormValue("connectors"), ",") {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			keys = append(keys, k)
		}
	}
	ctx := r.Context()
	if err := h.configs.Set(ctx, configs.KeyPluginTestEnabled, enabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.configs.Set(ctx, configs.KeyPluginTestConnectors, strings.Join(keys, ",")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/plugins/dev-mode", http.StatusFound)
}

func (h *Handler) pluginTestDecide(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := login.GetUser(ctx)
	var q entity.PluginTestRequest
	if err := h.repo.db.WithContext(ctx).First(&q, "id = ?", r.PathValue("id")).Error; err != nil {
		http.NotFound(w, r)
		return
	}
	now := time.Now()
	updates := map[string]any{"decided_by": admin.ID, "decided_at": now}
	switch r.PathValue("action") {
	case "approve":
		if q.Status != entity.PluginTestPending {
			http.Error(w, "only a pending request can be approved", http.StatusConflict)
			return
		}
		mins, _ := strconv.Atoi(r.FormValue("minutes"))
		if mins <= 0 {
			mins = 30
		}
		if mins > pluginTestMaxMinutes {
			mins = pluginTestMaxMinutes
		}
		updates["status"] = entity.PluginTestApproved
		updates["expires_at"] = now.Add(time.Duration(mins) * time.Minute)
	case "reject":
		if q.Status != entity.PluginTestPending {
			http.Error(w, "only a pending request can be rejected", http.StatusConflict)
			return
		}
		updates["status"] = entity.PluginTestRejected
	case "revoke":
		updates["status"] = entity.PluginTestRevoked
		updates["expires_at"] = now
	default:
		http.NotFound(w, r)
		return
	}
	if err := h.repo.db.WithContext(ctx).Model(&q).Updates(updates).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/plugins/dev-mode", http.StatusFound)
}

// ── CLI API (PAT bearer) ───────────────────────────────────────

type pluginTestCreateBody struct {
	ConnectorKey string `json:"connector_key"`
	// Instance is a connector instance id or its label (case-insensitive).
	Instance  string `json:"instance"`
	Operation string `json:"operation"`
	Host      string `json:"host"`
}

type pluginTestView struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"`
	State        string     `json:"state"`
	ConnectorKey string     `json:"connector_key"`
	InstanceID   string     `json:"instance_id"`
	Instance     string     `json:"instance"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	ApproveURL   string     `json:"approve_url"`
}

func (h *Handler) toPluginTestView(r *http.Request, q entity.PluginTestRequest) pluginTestView {
	base := strings.TrimRight(h.configs.Get(configs.KeyAppURL), "/")
	return pluginTestView{
		ID: q.ID, Status: q.Status, State: pluginTestState(q, time.Now()),
		ConnectorKey: q.ConnectorKey, InstanceID: q.InstanceID, Instance: q.InstanceLabel,
		ExpiresAt: q.ExpiresAt, ApproveURL: base + "/admin/plugins/dev-mode",
	}
}

// patUser authenticates the bearer PAT and returns an approved user.
func (h *Handler) patUser(r *http.Request) (*entity.User, error) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(tok) == "" {
		return nil, errors.New("missing bearer token (a wick personal access token, wick_pat_…)")
	}
	uid, err := h.tokens.Authenticate(r.Context(), strings.TrimSpace(tok))
	if err != nil {
		return nil, errors.New("invalid token")
	}
	u, err := h.auth.GetUserByID(r.Context(), uid)
	if err != nil || u == nil || !u.Approved {
		return nil, errors.New("user not found or not approved")
	}
	return u, nil
}

func writePluginTestJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func pluginTestErr(w http.ResponseWriter, status int, err error) {
	writePluginTestJSON(w, status, map[string]string{"error": err.Error()})
}

// resolvePluginTestInstance finds the instance the user may see by id or label.
func (h *Handler) resolvePluginTestInstance(ctx context.Context, u *entity.User, key, ref string) (*entity.Connector, error) {
	rows, err := h.connectors.ListVisibleTo(ctx, u.ID, h.auth.GetUserFilterTagIDs(ctx, u.ID), u.IsAdmin())
	if err != nil {
		return nil, err
	}
	ref = strings.TrimSpace(ref)
	var match []entity.Connector
	for _, c := range rows {
		if c.Key != key {
			continue
		}
		if c.ID == ref || strings.EqualFold(c.Label, ref) {
			match = append(match, c)
		}
	}
	switch len(match) {
	case 0:
		return nil, errors.New("no " + key + " instance named or with id " + strconv.Quote(ref) + " that you can access")
	case 1:
		return &match[0], nil
	default:
		return nil, errors.New("several " + key + " instances are labelled " + strconv.Quote(ref) + "; pass the instance id")
	}
}

func (h *Handler) apiPluginTestCreate(w http.ResponseWriter, r *http.Request) {
	u, err := h.patUser(r)
	if err != nil {
		pluginTestErr(w, http.StatusUnauthorized, err)
		return
	}
	var body pluginTestCreateBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		pluginTestErr(w, http.StatusBadRequest, errors.New("invalid JSON body"))
		return
	}
	body.ConnectorKey = strings.TrimSpace(body.ConnectorKey)
	if body.ConnectorKey == "" || strings.TrimSpace(body.Instance) == "" {
		pluginTestErr(w, http.StatusBadRequest, errors.New("connector_key and instance are required"))
		return
	}
	if err := h.pluginTestAllowed(body.ConnectorKey); err != nil {
		pluginTestErr(w, http.StatusForbidden, err)
		return
	}
	ctx := r.Context()
	inst, err := h.resolvePluginTestInstance(ctx, u, body.ConnectorKey, body.Instance)
	if err != nil {
		pluginTestErr(w, http.StatusNotFound, err)
		return
	}

	// Reuse a still-useful request for the same user + instance, so repeated
	// `go run` / dlv sessions inside one approval window need no new approval.
	var existing []entity.PluginTestRequest
	if err := h.repo.db.WithContext(ctx).
		Where("user_id = ? AND instance_id = ? AND status IN ?", u.ID, inst.ID,
			[]string{entity.PluginTestPending, entity.PluginTestApproved}).
		Order("created_at desc").Find(&existing).Error; err != nil {
		pluginTestErr(w, http.StatusInternalServerError, err)
		return
	}
	now := time.Now()
	for _, q := range existing {
		if q.Status == entity.PluginTestPending || q.Active(now) {
			writePluginTestJSON(w, http.StatusOK, h.toPluginTestView(r, q))
			return
		}
	}

	q := entity.PluginTestRequest{
		UserID:        u.ID,
		ConnectorKey:  body.ConnectorKey,
		InstanceID:    inst.ID,
		InstanceLabel: inst.Label,
		Operation:     truncateStr(body.Operation, 120),
		Host:          truncateStr(body.Host, 300),
		Status:        entity.PluginTestPending,
	}
	if err := h.repo.db.WithContext(ctx).Create(&q).Error; err != nil {
		pluginTestErr(w, http.StatusInternalServerError, err)
		return
	}
	writePluginTestJSON(w, http.StatusCreated, h.toPluginTestView(r, q))
}

func truncateStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ownPluginTest loads a request that belongs to the PAT owner.
func (h *Handler) ownPluginTest(w http.ResponseWriter, r *http.Request) (*entity.PluginTestRequest, *entity.User, bool) {
	u, err := h.patUser(r)
	if err != nil {
		pluginTestErr(w, http.StatusUnauthorized, err)
		return nil, nil, false
	}
	var q entity.PluginTestRequest
	if err := h.repo.db.WithContext(r.Context()).First(&q, "id = ?", r.PathValue("id")).Error; err != nil || q.UserID != u.ID {
		pluginTestErr(w, http.StatusNotFound, errors.New("request not found"))
		return nil, nil, false
	}
	return &q, u, true
}

func (h *Handler) apiPluginTestGet(w http.ResponseWriter, r *http.Request) {
	q, _, ok := h.ownPluginTest(w, r)
	if !ok {
		return
	}
	writePluginTestJSON(w, http.StatusOK, h.toPluginTestView(r, *q))
}

func (h *Handler) apiPluginTestConfig(w http.ResponseWriter, r *http.Request) {
	q, u, ok := h.ownPluginTest(w, r)
	if !ok {
		return
	}
	now := time.Now()
	if !q.Active(now) {
		pluginTestErr(w, http.StatusForbidden, errors.New("request is "+pluginTestState(*q, now)+", not approved"))
		return
	}
	// Re-check the switch and allowlist: turning either off cuts every
	// approved request immediately.
	if err := h.pluginTestAllowed(q.ConnectorKey); err != nil {
		pluginTestErr(w, http.StatusForbidden, err)
		return
	}
	ctx := r.Context()
	// And the requester must still be able to see the instance: losing access
	// after approval ends the borrow too, not only when the approval expires.
	if _, err := h.resolvePluginTestInstance(ctx, u, q.ConnectorKey, q.InstanceID); err != nil {
		pluginTestErr(w, http.StatusForbidden, errors.New("you no longer have access to this instance"))
		return
	}
	cfg, err := h.connectors.PlainConfigsForPluginTest(ctx, q.InstanceID, u.ID)
	if err != nil {
		pluginTestErr(w, http.StatusInternalServerError, err)
		return
	}
	_ = h.repo.db.WithContext(ctx).Model(q).Updates(map[string]any{
		"fetch_count":   q.FetchCount + 1,
		"last_fetch_at": now,
	}).Error
	writePluginTestJSON(w, http.StatusOK, map[string]any{
		"connector_key": q.ConnectorKey,
		"instance_id":   q.InstanceID,
		"expires_at":    q.ExpiresAt,
		"configs":       cfg,
	})
}
