package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/agents/clitoken"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp"
	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// Plugin test requests let a connector plugin run from source (go run / dlv,
// `--run-op`, see pkg/plugin/runop.go) against a real instance. The requester
// asks first ("test with this instance"), the request shows up on
// Admin → Plugins → Development mode, and an admin approves it for a limited
// time. Two ways to use an approval:
//
//   - a person (PAT, any machine) fetches the instance's plaintext config
//     into their own process — POST …/config;
//   - an agent session (wick_cli_ token, this machine only) never sees the
//     config: wick spawns the candidate binary itself, runs the op through
//     the normal connector pipeline (decrypt, access checks, masking, run
//     log) and returns the masked result — POST …/run.
//
// Every gate below must hold at the moment the request is used:
//
//  1. the master switch plugin_test_enabled (default off);
//  2. an admin approved this one request, and it has not expired or been
//     revoked;
//  3. the caller is the user who asked and can still see the instance;
//  4. the request is bound to everything about who filed it, and each use
//     must match all of it: the same bearer credential (that PAT, or that
//     agent session — another token, even of the same user, files its own
//     request), the same client secret (a random value the requesting
//     binary keeps in a local file), the same machine ("<os user>@<host>")
//     and the same client IP. Any mismatch is refused, never re-bound.

const (
	pluginTestMaxMinutes  = 240
	pluginTestHistoryDays = 7
	pluginTestHistoryRows = 100
)

func (h *Handler) pluginTestAllowed() error {
	if !strings.EqualFold(strings.TrimSpace(h.configs.Get(configs.KeyPluginTestEnabled)), "true") {
		return errors.New("plugin test requests are disabled on this wick (Admin → Plugins → Development mode)")
	}
	return nil
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

// pluginTestClientIP is the caller's IP as wick sees it (first hop of
// X-Forwarded-For behind nginx, else the socket peer).
func pluginTestClientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// pluginTestFromLoopback reports a direct loopback caller: no proxy headers,
// socket peer on 127.0.0.0/8 or ::1.
func pluginTestFromLoopback(r *http.Request) bool {
	for _, hd := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded"} {
		if strings.TrimSpace(r.Header.Get(hd)) != "" {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func hashPluginTestSecret(secret string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(secret)))
	return hex.EncodeToString(sum[:])
}

// ── admin page ─────────────────────────────────────────────────

func (h *Handler) pluginTestsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var reqs []entity.PluginTestRequest
	// History is a short decision log (pending / allowed / denied / expired),
	// not an archive: the last pluginTestHistoryDays days, capped.
	if err := h.repo.db.WithContext(ctx).
		Where("created_at > ? OR status = ?", time.Now().AddDate(0, 0, -pluginTestHistoryDays), entity.PluginTestPending).
		Order("created_at desc").Limit(pluginTestHistoryRows).Find(&reqs).Error; err != nil {
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
		Enabled: strings.EqualFold(h.configs.Get(configs.KeyPluginTestEnabled), "true"),
		Rows:    rows,
	}
	_ = view.PluginTestsPage(vm, login.GetUser(ctx)).Render(ctx, w)
}

func (h *Handler) pluginTestsSettings(w http.ResponseWriter, r *http.Request) {
	enabled := "false"
	if r.FormValue("enabled") == "true" {
		enabled = "true"
	}
	if err := h.configs.Set(r.Context(), configs.KeyPluginTestEnabled, enabled); err != nil {
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

// ── CLI API ────────────────────────────────────────────────────

type pluginTestCreateBody struct {
	ConnectorKey string `json:"connector_key"`
	// Instance is a connector instance id or its label (case-insensitive).
	Instance  string `json:"instance"`
	Operation string `json:"operation"`
	Host      string `json:"host"`
	// ClientSecret is the requesting machine's local secret; only its hash
	// is stored, and every later use must present it again.
	ClientSecret string `json:"client_secret"`
	// Machine is "<os user>@<hostname>" of the runner; later uses send it in
	// the X-Plugin-Test-Machine header.
	Machine string `json:"machine"`
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

func (h *Handler) toPluginTestView(q entity.PluginTestRequest) pluginTestView {
	base := strings.TrimRight(h.configs.Get(configs.KeyAppURL), "/")
	return pluginTestView{
		ID: q.ID, Status: q.Status, State: pluginTestState(q, time.Now()),
		ConnectorKey: q.ConnectorKey, InstanceID: q.InstanceID, Instance: q.InstanceLabel,
		ExpiresAt: q.ExpiresAt, ApproveURL: base + "/admin/plugins/dev-mode",
	}
}

// pluginTestCaller is who is calling the CLI API.
type pluginTestCaller struct {
	user *entity.User
	// agentSession is set when the bearer was a session-bound wick_cli_
	// token: an agent acting for the session's owner.
	agentSession string
	// credential identifies the bearer itself (see PluginTestRequest.Credential).
	credential string
}

// pluginTestAuth authenticates the caller. Two bearers are accepted: a
// personal access token (wick_pat_…, a person on any machine), or a
// session-bound wick_cli_ token (an agent session on THIS machine, acting as
// the session's owner — refused unless the request comes straight from
// loopback, the same rule as the CLI channel).
func (h *Handler) pluginTestAuth(r *http.Request) (pluginTestCaller, error) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	tok = strings.TrimSpace(tok)
	if !ok || tok == "" {
		return pluginTestCaller{}, errors.New("missing bearer token (a wick personal access token wick_pat_…, or a session wick_cli_ token)")
	}
	var c pluginTestCaller
	var uid string
	if strings.HasPrefix(tok, clitoken.Prefix) {
		if !pluginTestFromLoopback(r) {
			return pluginTestCaller{}, errors.New("a wick_cli_ token only works from this machine; use a personal access token from elsewhere")
		}
		grant, ok := clitoken.Resolve(tok)
		if !ok || grant.UserID == "" {
			return pluginTestCaller{}, errors.New("wick_cli_ token is unknown or expired — mint a fresh one")
		}
		uid, c.agentSession = grant.UserID, grant.SessionID
		c.credential = "session:" + grant.SessionID
	} else {
		pat, err := h.tokens.AuthenticateToken(r.Context(), tok)
		if err != nil {
			return pluginTestCaller{}, errors.New("invalid token")
		}
		uid, c.credential = pat.UserID, "pat:"+pat.ID
	}
	u, err := h.auth.GetUserByID(r.Context(), uid)
	if err != nil || u == nil || !u.Approved {
		return pluginTestCaller{}, errors.New("user not found or not approved")
	}
	c.user = u
	return c, nil
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

func truncateStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (h *Handler) apiPluginTestCreate(w http.ResponseWriter, r *http.Request) {
	caller, err := h.pluginTestAuth(r)
	if err != nil {
		pluginTestErr(w, http.StatusUnauthorized, err)
		return
	}
	u := caller.user
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
	if len(strings.TrimSpace(body.ClientSecret)) < 32 {
		pluginTestErr(w, http.StatusBadRequest, errors.New("client_secret is required (the run-op binary generates it; rebuild against a wick that has this feature)"))
		return
	}
	secretHash := hashPluginTestSecret(body.ClientSecret)
	machine := truncateStr(body.Machine, 200)
	if machine == "" {
		pluginTestErr(w, http.StatusBadRequest, errors.New("machine is required (the run-op binary sends <os user>@<hostname>; rebuild against a wick that has this feature)"))
		return
	}
	ip := truncateStr(pluginTestClientIP(r), 64)
	if err := h.pluginTestAllowed(); err != nil {
		pluginTestErr(w, http.StatusForbidden, err)
		return
	}
	ctx := r.Context()
	inst, err := h.resolvePluginTestInstance(ctx, u, body.ConnectorKey, body.Instance)
	if err != nil {
		pluginTestErr(w, http.StatusNotFound, err)
		return
	}

	// Reuse a still-useful request only when EVERYTHING matches (user,
	// instance, credential, secret, machine, IP), so repeated `go run` / dlv
	// sessions inside one approval window need no new approval. Anything
	// different — another token, another person, another machine or network
	// — files its own request and waits for its own approval.
	var existing []entity.PluginTestRequest
	if err := h.repo.db.WithContext(ctx).
		Where("user_id = ? AND instance_id = ? AND credential = ? AND secret_hash = ? AND machine = ? AND request_ip = ? AND status IN ?",
			u.ID, inst.ID, caller.credential, secretHash, machine, ip,
			[]string{entity.PluginTestPending, entity.PluginTestApproved}).
		Order("created_at desc").Find(&existing).Error; err != nil {
		pluginTestErr(w, http.StatusInternalServerError, err)
		return
	}
	now := time.Now()
	for _, q := range existing {
		if q.Status == entity.PluginTestPending || q.Active(now) {
			writePluginTestJSON(w, http.StatusOK, h.toPluginTestView(q))
			return
		}
	}

	host := body.Host
	if caller.agentSession != "" {
		host = "agent session " + caller.agentSession + " · " + host
	}
	q := entity.PluginTestRequest{
		UserID:        u.ID,
		ConnectorKey:  body.ConnectorKey,
		InstanceID:    inst.ID,
		InstanceLabel: inst.Label,
		Operation:     truncateStr(body.Operation, 120),
		Host:          truncateStr(host, 300),
		Machine:       machine,
		Credential:    caller.credential,
		RequestIP:     ip,
		SecretHash:    secretHash,
		Status:        entity.PluginTestPending,
	}
	if err := h.repo.db.WithContext(ctx).Create(&q).Error; err != nil {
		pluginTestErr(w, http.StatusInternalServerError, err)
		return
	}
	writePluginTestJSON(w, http.StatusCreated, h.toPluginTestView(q))
}

// ownPluginTest loads a request that belongs to the caller.
func (h *Handler) ownPluginTest(w http.ResponseWriter, r *http.Request) (*entity.PluginTestRequest, pluginTestCaller, bool) {
	caller, err := h.pluginTestAuth(r)
	if err != nil {
		pluginTestErr(w, http.StatusUnauthorized, err)
		return nil, caller, false
	}
	var q entity.PluginTestRequest
	if err := h.repo.db.WithContext(r.Context()).First(&q, "id = ?", r.PathValue("id")).Error; err != nil || q.UserID != caller.user.ID {
		pluginTestErr(w, http.StatusNotFound, errors.New("request not found"))
		return nil, caller, false
	}
	if q.Credential != caller.credential {
		pluginTestErr(w, http.StatusForbidden, errors.New("this request was filed with another token; file your own request"))
		return nil, caller, false
	}
	return &q, caller, true
}

func (h *Handler) apiPluginTestGet(w http.ResponseWriter, r *http.Request) {
	q, _, ok := h.ownPluginTest(w, r)
	if !ok {
		return
	}
	writePluginTestJSON(w, http.StatusOK, h.toPluginTestView(*q))
}

// usablePluginTest runs the per-use gates shared by …/config and …/run and
// records the use. It writes the error response itself.
func (h *Handler) usablePluginTest(w http.ResponseWriter, r *http.Request) (*entity.PluginTestRequest, pluginTestCaller, bool) {
	q, caller, ok := h.ownPluginTest(w, r)
	if !ok {
		return nil, caller, false
	}
	now := time.Now()
	if !q.Active(now) {
		pluginTestErr(w, http.StatusForbidden, errors.New("request is "+pluginTestState(*q, now)+", not approved"))
		return nil, caller, false
	}
	// The approval belongs to the machine that asked.
	got := hashPluginTestSecret(r.Header.Get("X-Plugin-Test-Secret"))
	if q.SecretHash == "" || subtle.ConstantTimeCompare([]byte(got), []byte(q.SecretHash)) != 1 {
		pluginTestErr(w, http.StatusForbidden, errors.New("this approval belongs to the machine that filed the request (client secret mismatch)"))
		return nil, caller, false
	}
	// Same machine and same network as the request, every time.
	if strings.TrimSpace(r.Header.Get("X-Plugin-Test-Machine")) != q.Machine {
		pluginTestErr(w, http.StatusForbidden, errors.New("this approval belongs to "+q.Machine+" (machine mismatch); file a new request from this machine"))
		return nil, caller, false
	}
	if ip := truncateStr(pluginTestClientIP(r), 64); ip != q.RequestIP {
		pluginTestErr(w, http.StatusForbidden, errors.New("this approval belongs to IP "+q.RequestIP+", not "+ip+"; file a new request"))
		return nil, caller, false
	}
	// The switch is re-checked on every use: turning it off cuts every
	// approved request at once.
	if err := h.pluginTestAllowed(); err != nil {
		pluginTestErr(w, http.StatusForbidden, err)
		return nil, caller, false
	}
	// And the requester must still be able to see the instance.
	if _, err := h.resolvePluginTestInstance(r.Context(), caller.user, q.ConnectorKey, q.InstanceID); err != nil {
		pluginTestErr(w, http.StatusForbidden, errors.New("you no longer have access to this instance"))
		return nil, caller, false
	}
	_ = h.repo.db.WithContext(r.Context()).Model(q).Updates(map[string]any{
		"fetch_count":   q.FetchCount + 1,
		"last_fetch_at": now,
		"last_fetch_ip": truncateStr(pluginTestClientIP(r), 64),
	}).Error
	return q, caller, true
}

// apiPluginTestConfig hands the instance's plaintext config to a PERSON's
// process. Agent sessions are refused: they use …/run, which keeps the
// config inside wick.
func (h *Handler) apiPluginTestConfig(w http.ResponseWriter, r *http.Request) {
	if tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); strings.HasPrefix(strings.TrimSpace(tok), clitoken.Prefix) {
		pluginTestErr(w, http.StatusForbidden, errors.New("agent sessions never receive plaintext config; run the op through POST /api/plugin-tests/{id}/run"))
		return
	}
	q, caller, ok := h.usablePluginTest(w, r)
	if !ok {
		return
	}
	cfg, err := h.connectors.PlainConfigsForPluginTest(r.Context(), q.InstanceID, caller.user.ID)
	if err != nil {
		pluginTestErr(w, http.StatusInternalServerError, err)
		return
	}
	writePluginTestJSON(w, http.StatusOK, map[string]any{
		"connector_key": q.ConnectorKey,
		"instance_id":   q.InstanceID,
		"expires_at":    q.ExpiresAt,
		"configs":       cfg,
	})
}

type pluginTestRunBody struct {
	// Binary is the absolute path of the candidate plugin binary on this
	// machine (the run-op process sends its own executable).
	Binary    string         `json:"binary"`
	Operation string         `json:"operation"`
	Input     map[string]any `json:"input"`
}

// apiPluginTestRun spawns the candidate binary as a plugin and runs one op
// through the normal connector pipeline against the request's instance. The
// caller gets the masked result; the config never leaves wick. Loopback only:
// the binary path names a file on this machine.
func (h *Handler) apiPluginTestRun(w http.ResponseWriter, r *http.Request) {
	if !pluginTestFromLoopback(r) {
		pluginTestErr(w, http.StatusForbidden, errors.New("…/run spawns a binary on this machine and is reachable from this machine only"))
		return
	}
	var body pluginTestRunBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		pluginTestErr(w, http.StatusBadRequest, errors.New("invalid JSON body"))
		return
	}
	q, caller, ok := h.usablePluginTest(w, r)
	if !ok {
		return
	}
	bin := filepath.Clean(strings.TrimSpace(body.Binary))
	if !filepath.IsAbs(bin) {
		pluginTestErr(w, http.StatusBadRequest, errors.New("binary must be an absolute path"))
		return
	}
	if st, err := os.Stat(bin); err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
		pluginTestErr(w, http.StatusBadRequest, errors.New("binary is not an executable file on this machine"))
		return
	}

	ctx := r.Context()
	client, raw, err := connplugin.Spawn(connplugin.SpawnSpec{
		Binary:   bin,
		Plugins:  wickplugin.VersionedPluginsFor(q.ConnectorKey),
		Dispense: wickplugin.PluginName,
	})
	if err != nil {
		pluginTestErr(w, http.StatusBadGateway, fmt.Errorf("start candidate plugin: %w", err))
		return
	}
	defer client.Kill()
	conn, ok := raw.(wickplugin.GRPCConn)
	if !ok {
		pluginTestErr(w, http.StatusBadGateway, errors.New("candidate binary is not a connector plugin"))
		return
	}
	manifest, err := conn.Schema(ctx)
	if err != nil {
		pluginTestErr(w, http.StatusBadGateway, fmt.Errorf("read candidate manifest: %w", err))
		return
	}
	var mod connector.Module
	if err := json.Unmarshal(manifest, &mod); err != nil {
		pluginTestErr(w, http.StatusBadGateway, fmt.Errorf("decode candidate manifest: %w", err))
		return
	}
	if mod.Meta.Key != q.ConnectorKey {
		pluginTestErr(w, http.StatusBadRequest, fmt.Errorf("candidate binary is connector %q, the request is for %q", mod.Meta.Key, q.ConnectorKey))
		return
	}
	for _, op := range mod.AllOps() {
		if op.Key == body.Operation && op.Destructive {
			pluginTestErr(w, http.StatusForbidden, fmt.Errorf("%s is a destructive op; plugin tests only run non-destructive ops", op.Key))
			return
		}
	}
	mod = connplugin.BuildModule(mod, func(string) (*connplugin.Lease, error) {
		return &connplugin.Lease{Conn: conn}, nil
	})

	input := make(map[string]string, len(body.Input))
	for k, v := range body.Input {
		switch t := v.(type) {
		case string:
			input[k] = t
		case nil:
		default:
			b, _ := json.Marshal(t)
			input[k] = string(b)
		}
	}
	if caller.agentSession != "" {
		// An agent runs under its own access list, not its owner's.
		ctx = mcp.WithAgentScope(ctx, caller.agentSession)
	}
	res, err := h.connectors.Execute(ctx, connectors.ExecuteParams{
		ConnectorID:    q.InstanceID,
		OperationKey:   body.Operation,
		Input:          input,
		RawInput:       body.Input,
		Source:         entity.ConnectorRunSourcePluginTest,
		UserID:         caller.user.ID,
		IPAddress:      pluginTestClientIP(r),
		UserAgent:      "plugin-test " + q.ID,
		SessionID:      caller.agentSession,
		IsAdmin:        caller.user.IsAdmin(),
		TagIDs:         h.auth.GetUserFilterTagIDs(ctx, caller.user.ID),
		ModuleOverride: &mod,
	})
	out := map[string]any{"request_id": q.ID}
	if res != nil {
		out["run_id"] = res.RunID
		out["status"] = res.Status
		out["latency_ms"] = res.LatencyMs
		if res.ResponseJSON != "" {
			out["result"] = json.RawMessage(res.ResponseJSON)
		}
		if res.ErrorMessage != "" {
			out["error"] = res.ErrorMessage
		}
	}
	if err != nil {
		if _, has := out["error"]; !has {
			out["error"] = err.Error()
		}
		writePluginTestJSON(w, http.StatusUnprocessableEntity, out)
		return
	}
	writePluginTestJSON(w, http.StatusOK, out)
}
