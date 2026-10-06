package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
)

// callbackKey is the reserved /x/{key} segment of the callback API a
// service plugin reaches with its WICK_PLUGIN_TOKEN: /x/-/api/...
// ("-" is never a valid plugin key).
const callbackKey = "-"

// ScopeWhoami needs no grant: every callback token may ask who it is.
const ScopeWhoami = "whoami"

type callbackRoute struct {
	scope string
	h     http.Handler
}

type ctxKey struct{}

// Caller is the plugin a callback request came from.
type Caller struct {
	Key    string
	Scopes []string
}

// CallerFrom returns the plugin behind a callback request.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(ctxKey{}).(Caller)
	return c, ok
}

// HandleCallback exposes h at /x/-{path} ("METHOD /api/..."), reachable only
// with a callback token whose plugin declared scope in callback_scopes.
func (h *Host) HandleCallback(pattern, scope string, fn http.Handler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cb == nil {
		h.cb = map[string]callbackRoute{}
	}
	h.cb[pattern] = callbackRoute{scope: scope, h: fn}
}

func (h *Host) serveCallbackAPI(w http.ResponseWriter, r *http.Request, sub string) {
	key, scopes, ok := "", []string(nil), false
	if h.Tokens != nil {
		key, scopes, ok = h.Tokens.LookupCallback(bearer(r))
	}
	if !ok {
		http.Error(w, "invalid or revoked plugin token", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet && sub == "/api/whoami" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"plugin": key, "scopes": scopes})
		return
	}
	h.mu.RLock()
	route, found := h.cb[r.Method+" "+sub]
	h.mu.RUnlock()
	if !found {
		http.NotFound(w, r)
		return
	}
	if route.scope != ScopeWhoami && !slices.Contains(scopes, route.scope) {
		http.Error(w, "plugin token lacks scope "+strings.TrimSpace(route.scope), http.StatusForbidden)
		return
	}
	r.Header.Del("Authorization")
	route.h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, Caller{Key: key, Scopes: scopes})))
}
