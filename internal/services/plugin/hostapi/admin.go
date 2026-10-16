package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// AdminBase is where the admin JSON API of the Host access page lives. The
// path holds "plugin", so a plugin can never declare it in host_resources
// (RestPathReserved).
const (
	AdminBase      = "/manager/api/plugin-host-access"
	AdminCallsPath = "/manager/api/plugin-host-calls"
)

// HostAPINotStarted answers every admin route when the Host API failed to
// start.
const HostAPINotStarted = "host API not started"

// unknownScopeNote explains a declared scope this wick does not know.
const unknownScopeNote = "declared by the plugin but not known to this wick version"

// AdminDirectory is what the admin page needs from the plugin directory:
// every installed plugin, plus the scopes ManifestDirectory flagged unknown.
type AdminDirectory interface {
	PluginDirectory
	All() []PluginInfo
}

// CallFilter narrows the audit list.
type CallFilter struct {
	PluginKey  string
	FailedOnly bool
	Since      time.Time
	Offset     int
	Limit      int
}

// CallStat counts one (plugin, scope)'s calls since a point in time.
type CallStat struct {
	PluginKey string
	Scope     string
	Calls     int64
	// Failed counts every call that did not succeed; Denied only those
	// refused with PermissionDenied.
	Failed int64
	Denied int64
	Last   time.Time
}

// CallReader reads the audit back for the admin page. GormStore implements
// it.
type CallReader interface {
	ListCalls(ctx context.Context, f CallFilter) ([]entity.PluginHostCall, int64, error)
	CallStats(ctx context.Context, since time.Time) ([]CallStat, error)
}

// Admin serves the "Plugins → Host access" page: what every plugin declares,
// the overlay decisions, the audit, and the emergency switches.
type Admin struct {
	Plugins AdminDirectory
	Perms   *Permissions
	Calls   CallReader
	// Op reports a connector op's destructive flag (ConnectorRunner.Op);
	// nil = connector items are never marked destructive.
	Op func(connector, op string) (destructive, found bool)
	// Revoke revokes (true) or restores (false) all Host API access of a
	// plugin, persistently and over both doors (Bridge.SetRevoked); nil =
	// the host API is not running. dropped counts live process tokens
	// dropped; changed is false when nothing changed.
	Revoke func(key string, revoked bool) (dropped int, changed bool, err error)
	// Revoked reports the revoke flag (Bridge.Revoked); nil = never.
	Revoked func(key string) bool
	// Actor is the signed-in admin making a change ("" when unknown).
	Actor func(*http.Request) string

	now func() time.Time
}

// AccessItem is one declared item of a scope.
type AccessItem struct {
	Item        string `json:"item"`
	Blocked     bool   `json:"blocked"`
	Destructive bool   `json:"destructive"`
}

// AccessScope is one scope row of a plugin.
type AccessScope struct {
	Scope        string       `json:"scope"`
	Label        string       `json:"label,omitempty"`
	Description  string       `json:"description,omitempty"`
	Declared     bool         `json:"declared"`
	Known        bool         `json:"known"`
	Status       string       `json:"status"`
	Reason       string       `json:"reason,omitempty"`
	Overlay      string       `json:"overlay,omitempty"` // "allow", "block" or "" (default)
	DefaultAllow bool         `json:"default_allow"`
	Destructive  bool         `json:"destructive"`
	Sensitive    bool         `json:"sensitive"`
	Declarative  bool         `json:"declarative"`
	Resource     string       `json:"resource,omitempty"`
	Items        []AccessItem `json:"items,omitempty"`
	Calls        int64        `json:"calls_24h"`
	Failed       int64        `json:"failed_24h"`
	Denied       int64        `json:"denied_24h"`
}

// PluginAccess is one plugin on the Host access page and the "What it can
// do" card.
type PluginAccess struct {
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	Version    string `json:"version"`
	Declares   bool   `json:"declares"`
	BlockedAll bool   `json:"blocked_all"`
	// Revoked: an admin revoked all Host API access (tokens and broker)
	// until it is restored.
	Revoked   bool          `json:"revoked"`
	Scopes    []AccessScope `json:"scopes"`
	ItemCount int           `json:"item_count"`
	Calls     int64         `json:"calls_24h"`
	Failed    int64         `json:"failed_24h"`
	Denied    int64         `json:"denied_24h"`
	LastCall  *time.Time    `json:"last_call,omitempty"`
}

// CallView is one audit row.
type CallView struct {
	Ts         time.Time `json:"ts"`
	PluginKey  string    `json:"plugin"`
	Kind       string    `json:"kind"`
	RPC        string    `json:"rpc"`
	Scope      string    `json:"scope"`
	Item       string    `json:"item,omitempty"`
	UserID     string    `json:"user_id,omitempty"`
	Source     string    `json:"source,omitempty"`
	OK         bool      `json:"ok"`
	Code       string    `json:"code,omitempty"`
	DurationMs int64     `json:"duration_ms"`
}

func (a *Admin) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// scopeItems lists the declared items a call under scope can reach: rest
// and db split their items between the read and the write scope.
func scopeItems(r *wickplugin.HostResources, sc wickplugin.HostScope) []string {
	items := r.Items(sc.Resource)
	switch sc.Name {
	case wickplugin.ScopeHostRestRead, wickplugin.ScopeHostRestWrite:
		read := sc.Name == wickplugin.ScopeHostRestRead
		return slices.DeleteFunc(items, func(it string) bool { return strings.HasPrefix(it, "GET ") != read })
	case wickplugin.ScopeHostDBRead:
		return slices.DeleteFunc(items, func(it string) bool { return !strings.HasSuffix(it, ":read") })
	case wickplugin.ScopeHostDBWrite:
		return slices.DeleteFunc(items, func(it string) bool { return !strings.HasSuffix(it, ":write") })
	}
	return items
}

// itemDestructive marks the items that change data: every write item, and
// connector ops the connector catalog flags destructive.
func (a *Admin) itemDestructive(r *wickplugin.HostResources, sc wickplugin.HostScope, item string) bool {
	switch sc.Name {
	case wickplugin.ScopeHostRestWrite, wickplugin.ScopeHostDBWrite:
		return true
	case wickplugin.ScopeHostConnectorsExec:
		if a.Op == nil || r == nil {
			return false
		}
		for _, c := range r.Connectors {
			for _, op := range c.Ops {
				if wickplugin.ConnectorItem(c.Connector, op) == item {
					d, _ := a.Op(c.Connector, op)
					return d
				}
			}
		}
	}
	return false
}

// declares reports whether p asks for any Host API access at all.
func declares(p PluginInfo) bool { return len(p.Scopes) > 0 || !p.Resources.Empty() }

// stats groups CallStats rows by plugin and scope.
type stats map[string]map[string]CallStat

func (a *Admin) stats(ctx context.Context, since time.Time) stats {
	out := stats{}
	if a.Calls == nil {
		return out
	}
	rows, err := a.Calls.CallStats(ctx, since)
	if err != nil {
		return out
	}
	for _, r := range rows {
		if out[r.PluginKey] == nil {
			out[r.PluginKey] = map[string]CallStat{}
		}
		out[r.PluginKey][r.Scope] = r
	}
	return out
}

// view builds p's access: the declared scopes (official ones in table order,
// then the unknown ones), plus any scope that was used in the audit window
// or has an overlay row without being declared.
func (a *Admin) view(ctx context.Context, p PluginInfo, day, week map[string]CallStat) PluginAccess {
	ov, _ := a.Perms.overlay(ctx, p.Key)
	out := PluginAccess{Key: p.Key, Kind: p.Kind, Version: p.Version, Declares: declares(p), Scopes: []AccessScope{}}
	if allowed, ok := ov[permKey{BlockAll, BlockAll}]; ok && !allowed {
		out.BlockedAll = true
	}
	if a.Revoked != nil {
		out.Revoked = a.Revoked(p.Key)
	}
	for _, sc := range wickplugin.HostScopes {
		declared := slices.Contains(p.Scopes, sc.Name)
		_, overlaid := ov[permKey{sc.Name, ""}]
		_, used := week[sc.Name]
		if !declared && !overlaid && !used {
			continue
		}
		d := a.Perms.Decide(ctx, p, sc.Name, "")
		row := AccessScope{Scope: sc.Name, Label: sc.Label, Description: sc.Description, Declared: declared, Known: true,
			Status: d.Status, Reason: d.Reason, DefaultAllow: sc.DefaultAllow, Destructive: sc.Destructive,
			Sensitive: sc.Sensitive, Declarative: sc.Declarative, Resource: sc.Resource,
			Calls: day[sc.Name].Calls, Failed: day[sc.Name].Failed, Denied: day[sc.Name].Denied}
		if v, ok := ov[permKey{sc.Name, ""}]; ok {
			row.Overlay = map[bool]string{true: "allow", false: "block"}[v]
		}
		if sc.Resource != "" {
			for _, it := range scopeItems(p.Resources, sc) {
				blocked := false
				if v, ok := ov[permKey{sc.Name, it}]; ok && !v {
					blocked = true
				}
				row.Items = append(row.Items, AccessItem{Item: it, Blocked: blocked, Destructive: a.itemDestructive(p.Resources, sc, it)})
			}
			out.ItemCount += len(row.Items)
		}
		out.Scopes = append(out.Scopes, row)
	}
	for _, s := range p.Scopes {
		if _, ok := wickplugin.LookupHostScope(s); !ok {
			out.Scopes = append(out.Scopes, AccessScope{Scope: s, Declared: true, Status: StatusUnknown, Reason: unknownScopeNote})
		}
	}
	for _, st := range day {
		out.Calls += st.Calls
		out.Failed += st.Failed
		out.Denied += st.Denied
	}
	for _, st := range week {
		if out.LastCall == nil || st.Last.After(*out.LastCall) {
			t := st.Last
			out.LastCall = &t
		}
	}
	return out
}

// List returns every plugin that declares Host API access, was seen calling
// it in the audit window, or is blocked; sorted by key.
func (a *Admin) List(ctx context.Context) []PluginAccess {
	now := a.clock()
	day, week := a.stats(ctx, now.Add(-24*time.Hour)), a.stats(ctx, now.Add(-AuditRetention))
	out := []PluginAccess{}
	for _, p := range a.Plugins.All() {
		v := a.view(ctx, p, day[p.Key], week[p.Key])
		if v.Declares || len(week[p.Key]) > 0 || v.BlockedAll || v.Revoked {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Get returns one plugin's access (any kind, declaring or not: the detail
// card shows "no host access" for the latter).
func (a *Admin) Get(ctx context.Context, key string) (PluginAccess, bool) {
	p, ok := a.Plugins.Plugin(key)
	if !ok {
		return PluginAccess{}, false
	}
	now := a.clock()
	day, week := a.stats(ctx, now.Add(-24*time.Hour)), a.stats(ctx, now.Add(-AuditRetention))
	return a.view(ctx, p, day[key], week[key]), true
}

// RegisterAdmin mounts the Host access API; wrap is the admin middleware
// (the same one every other plugin admin route uses).
func (a *Admin) RegisterAdmin(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	hf := func(f http.HandlerFunc) http.Handler { return wrap(f) }
	mux.Handle("GET "+AdminBase, hf(func(w http.ResponseWriter, r *http.Request) {
		writeAdminJSON(w, http.StatusOK, a.List(r.Context()))
	}))
	mux.Handle("GET "+AdminBase+"/{key}", hf(a.withPlugin(func(w http.ResponseWriter, r *http.Request, p PluginInfo) {
		a.reply(w, r, p.Key)
	})))
	mux.Handle("POST "+AdminBase+"/{key}/scope", hf(a.withPlugin(a.serveScope)))
	mux.Handle("POST "+AdminBase+"/{key}/item", hf(a.withPlugin(a.serveItem)))
	mux.Handle("POST "+AdminBase+"/{key}/block-all", hf(a.withPlugin(a.serveBlockAll)))
	mux.Handle("POST "+AdminBase+"/{key}/revoke", hf(a.withPlugin(a.serveRevoke)))
	mux.Handle("GET "+AdminCallsPath, hf(a.serveCalls))
}

func (a *Admin) withPlugin(fn func(http.ResponseWriter, *http.Request, PluginInfo)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.Plugins.Plugin(r.PathValue("key"))
		if !ok {
			adminError(w, http.StatusNotFound, "no such plugin")
			return
		}
		fn(w, r, p)
	}
}

func (a *Admin) reply(w http.ResponseWriter, r *http.Request, key string) {
	v, ok := a.Get(r.Context(), key)
	if !ok {
		adminError(w, http.StatusNotFound, "no such plugin")
		return
	}
	writeAdminJSON(w, http.StatusOK, v)
}

func (a *Admin) actor(r *http.Request) string {
	if a.Actor == nil {
		return ""
	}
	return a.Actor(r)
}

// knownScope accepts an official scope; unknown ones cannot be toggled
// (they are always denied).
func knownScope(name string) (wickplugin.HostScope, bool) {
	if name == wickplugin.ScopeHostWhoami {
		return wickplugin.HostScope{}, false // always allowed, nothing to decide
	}
	return wickplugin.LookupHostScope(name)
}

// serveScope sets a scope decision: {"scope", "allowed": true|false|null};
// null clears the overlay back to the scope's default.
func (a *Admin) serveScope(w http.ResponseWriter, r *http.Request, p PluginInfo) {
	var body struct {
		Scope   string `json:"scope"`
		Allowed *bool  `json:"allowed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		adminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	sc, ok := knownScope(body.Scope)
	if !ok {
		adminError(w, http.StatusBadRequest, "unknown scope")
		return
	}
	if sc.Declarative {
		adminError(w, http.StatusBadRequest, "declarative scope: nothing to allow or block")
		return
	}
	// A resource scope the plugin did not declare stays denied whatever the
	// overlay says; only clearing a leftover row is accepted.
	if body.Allowed != nil && sc.Resource != "" && !slices.Contains(p.Scopes, sc.Name) {
		adminError(w, http.StatusBadRequest, "scope not declared by the plugin")
		return
	}
	var err error
	if body.Allowed == nil {
		err = a.Perms.Clear(r.Context(), p.Key, body.Scope, "")
	} else {
		err = a.Perms.Set(r.Context(), p.Key, body.Scope, "", *body.Allowed, a.actor(r))
	}
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.reply(w, r, p.Key)
}

// serveItem blocks (✕) or allows again (↺) one declared item:
// {"scope", "item", "blocked"}. Allowing removes the block row, so the item
// follows its scope again.
func (a *Admin) serveItem(w http.ResponseWriter, r *http.Request, p PluginInfo) {
	var body struct {
		Scope   string `json:"scope"`
		Item    string `json:"item"`
		Blocked bool   `json:"blocked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		adminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	sc, ok := knownScope(body.Scope)
	if !ok || sc.Resource == "" {
		adminError(w, http.StatusBadRequest, "scope has no items")
		return
	}
	if !slices.Contains(scopeItems(p.Resources, sc), body.Item) {
		adminError(w, http.StatusBadRequest, "item not declared by the plugin")
		return
	}
	var err error
	if body.Blocked {
		err = a.Perms.Set(r.Context(), p.Key, body.Scope, body.Item, false, a.actor(r))
	} else {
		err = a.Perms.Clear(r.Context(), p.Key, body.Scope, body.Item)
	}
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.reply(w, r, p.Key)
}

// serveRevoke revokes or restores all Host API access: {"revoked": bool},
// an empty body revokes. The reply carries the plugin's access, how many
// live tokens were dropped and whether anything changed, so the page can
// say so honestly.
func (a *Admin) serveRevoke(w http.ResponseWriter, r *http.Request, p PluginInfo) {
	if a.Revoke == nil {
		adminError(w, http.StatusServiceUnavailable, HostAPINotStarted)
		return
	}
	body := struct {
		Revoked *bool `json:"revoked"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		adminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	revoked := body.Revoked == nil || *body.Revoked
	dropped, changed, err := a.Revoke(p.Key, revoked)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v, _ := a.Get(r.Context(), p.Key)
	writeAdminJSON(w, http.StatusOK, map[string]any{"access": v, "dropped": dropped, "changed": changed})
}

// serveBlockAll flips "Block all host access": {"blocked": bool}. Unblock
// removes the row; the scope and item decisions underneath are untouched.
func (a *Admin) serveBlockAll(w http.ResponseWriter, r *http.Request, p PluginInfo) {
	var body struct {
		Blocked bool `json:"blocked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		adminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	var err error
	if body.Blocked {
		err = a.Perms.Set(r.Context(), p.Key, BlockAll, BlockAll, false, a.actor(r))
	} else {
		err = a.Perms.Clear(r.Context(), p.Key, BlockAll, BlockAll)
	}
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.reply(w, r, p.Key)
}

// Audit list paging bounds.
const (
	callsPageDefault = 50
	callsPageMax     = 200
)

// serveCalls lists the audit, newest first: ?plugin=&failed=1&page=&limit=.
func (a *Admin) serveCalls(w http.ResponseWriter, r *http.Request) {
	if a.Calls == nil {
		adminError(w, http.StatusServiceUnavailable, "audit is not available")
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(page, 1)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = callsPageDefault
	}
	limit = min(limit, callsPageMax)
	f := CallFilter{PluginKey: q.Get("plugin"), FailedOnly: q.Get("failed") == "1" || q.Get("failed") == "true",
		Since: a.clock().Add(-AuditRetention), Offset: (page - 1) * limit, Limit: limit}
	rows, total, err := a.Calls.ListCalls(r.Context(), f)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	calls := make([]CallView, 0, len(rows))
	for _, c := range rows {
		calls = append(calls, CallView{Ts: c.Ts, PluginKey: c.PluginKey, Kind: c.Kind, RPC: c.RPC, Scope: c.Scope, Item: c.Item,
			UserID: c.UserID, Source: c.Source, OK: c.OK, Code: c.Code, DurationMs: c.DurationMs})
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"calls": calls, "total": total, "page": page, "limit": limit})
}

// RegisterUnavailableAdmin mounts the admin routes answering 503
// HostAPINotStarted, for a wick whose Host API did not start; wrap is the
// admin middleware.
func RegisterUnavailableAdmin(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	h := wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		adminError(w, http.StatusServiceUnavailable, HostAPINotStarted)
	}))
	mux.Handle("GET "+AdminBase, h)
	mux.Handle("GET "+AdminBase+"/{key}", h)
	mux.Handle("POST "+AdminBase+"/{key}/{action}", h)
	mux.Handle("GET "+AdminCallsPath, h)
}

func writeAdminJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func adminError(w http.ResponseWriter, code int, msg string) {
	writeAdminJSON(w, code, map[string]string{"error": msg})
}
