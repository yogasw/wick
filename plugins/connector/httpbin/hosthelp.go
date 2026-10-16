package main

import (
	"context"
	"errors"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// hostScopes is what this plugin declares for the Host API (see main):
// users.read is enough for Whoami + GetUser, and needs no host_resources.
var hostScopes = []string{wickplugin.ScopeHostUsersRead}

// hostFor returns wick's Host API; tests replace it.
var hostFor = func(ctx context.Context) (host.Host, error) { return wickplugin.Host(ctx) }

// whoamiHostResult is what whoami_host returns.
type whoamiHostResult struct {
	Available     bool     `json:"host_api_available"`
	Plugin        string   `json:"plugin,omitempty"`
	ScopesAllowed []string `json:"scopes_allowed,omitempty"`
	CallerID      string   `json:"caller_id,omitempty"`
	CallerName    string   `json:"caller_name,omitempty"`
	CallerEmail   string   `json:"caller_email,omitempty"`
}

// doWhoamiHost is a live sample of the Host API: Whoami for the plugin
// itself, then GetUser for the person running the op. An older wick (no Host
// API) is a normal answer, not an error.
func doWhoamiHost(c *connector.Ctx) (any, error) {
	ctx := c.Context()
	h, err := hostFor(ctx)
	if errors.Is(err, host.ErrHostAPIUnavailable) {
		return whoamiHostResult{}, nil
	} else if err != nil {
		return nil, err
	}
	who, err := h.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	out := whoamiHostResult{Available: true, Plugin: who.GetPluginKey(), ScopesAllowed: who.GetScopesAllowed(), CallerID: c.CallerUserID()}
	if out.CallerID == "" {
		return out, nil
	}
	u, err := h.GetUser(ctx, &hostpb.GetUserRequest{Id: out.CallerID})
	if err != nil {
		return nil, err // a *host.PermissionDeniedError names the blocked scope
	}
	out.CallerName, out.CallerEmail = u.GetName(), u.GetEmail()
	return out, nil
}
