// Package hostapi serves the plugin Host API (wick.host.v1): the calls a
// plugin makes back into wick. One Server answers both doors — the gRPC
// broker (Server is a hostpb.HostServer) and the HTTP door (HTTPHandler) —
// so permissions, identity, audit and the users cache are identical.
//
// The doors authenticate the plugin and put a Caller on the context
// (WithCaller); everything after that lives here.
package hostapi

import "context"

// Where Caller.UserID came from.
const (
	// SourceCaller: the person who opened or triggered the plugin.
	SourceCaller = "caller"
	// SourceToken: the owner of the access token the request carried.
	SourceToken = "token"
	// SourcePAT: the owner of the PAT stored in the plugin's wick_pat config.
	SourcePAT = "pat"
)

// Caller is the plugin making a Host API call, as the door authenticated it.
type Caller struct {
	PluginKey string
	Kind      string // connector | tool | job | service
	// UserID is the person the call runs for ("" = none known; user-level
	// calls then fall back to the plugin's wick_pat).
	UserID string
	Source string
	// SessionID is the wick session the plugin was triggered from, when
	// there is one. Reserved: the doors fill it in W3 so session/ticket
	// access can apply the calling agent's own data scope.
	SessionID string
	// CallbackScopes are a service plugin's legacy callback_scopes, echoed
	// as "scopes" by the HTTP whoami for plugins that predate the Host API.
	CallbackScopes []string
}

type callerKey struct{}

// WithCaller returns ctx carrying c. Both doors call it before handing the
// request to the Server.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the Caller on ctx.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok && c.PluginKey != ""
}
