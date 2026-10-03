// Package service is the public API of service plugins: an always-on HTTP
// handler the host mounts at /x/{key}/* (auth decided per route by the
// host), optionally acting as a Team remote agent source (remote_source).
//
// A service plugin's main is one call:
//
//	func main() { service.ServeService(module) }
//
// Paths seen by the handler are relative to /x/{key}; the host strips the
// prefix and sets X-Wick-Base to it.
package service

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// Route auth modes (see wickplugin.Auth*).
const (
	Public  = wickplugin.AuthPublic
	Token   = wickplugin.AuthToken
	Session = wickplugin.AuthSession
)

// Route is one path prefix under /x/{key} and who may reach it.
type Route = wickplugin.ServiceRoute

// Meta names the service.
type Meta struct {
	Key         string
	Name        string
	Description string
	Icon        string
}

// Module is one service plugin.
type Module struct {
	Meta    Meta
	Routes  []Route
	Configs []entity.Config
	// CallbackScopes are the wick REST scopes the plugin's callback token
	// may use (see Env.Callback).
	CallbackScopes []string
	// Register mounts the HTTP routes on mux (Go 1.22 patterns).
	Register func(mux *http.ServeMux, env *Env)
	// RemoteSource, when set, makes the plugin selectable as a Team remote
	// agent source ("Plugin" in the Remote agent wizard).
	RemoteSource RemoteSource
}

// RemoteTurn, RemoteEvent and RemoteSendResult are the wire types of the
// remote_source RPC (event schema v1).
type (
	RemoteTurn       = wickplugin.RemoteTurn
	RemoteEvent      = wickplugin.RemoteEvent
	RemoteSendResult = wickplugin.RemoteSendResult
)

// RemoteSource is what a service plugin implements to act as a Team remote
// agent: Send a turn, Receive its events (closed after a terminal done/error
// event), Done to release what Send set up.
type RemoteSource interface {
	Send(ctx context.Context, turn RemoteTurn) (RemoteSendResult, error)
	Receive(ctx context.Context, handle string) (<-chan RemoteEvent, error)
	Done(handle string)
}

// Describer optionally names the remote agent in the wizard.
type Describer interface {
	Describe() (name, detail string)
}

// Env is what the host hands a running service plugin: its config (pushed
// over Configure, updated live) and the callback credentials.
type Env struct {
	key string
	mu  sync.RWMutex
	cfg map[string]string
}

func newEnv(key string) *Env { return &Env{key: key, cfg: map[string]string{}} }

// Key is the plugin key.
func (e *Env) Key() string { return e.key }

// Cfg returns config value k as last pushed by the host.
func (e *Env) Cfg(k string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg[k]
}

func (e *Env) setCfg(v map[string]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg = v
}

// Callback returns wick's base URL and the plugin's scoped token. Send the
// token as "Authorization: Bearer <token>"; never log it. Both are empty
// outside wick.
func (e *Env) Callback() (baseURL, token string) {
	return strings.TrimRight(os.Getenv(wickplugin.EnvBaseURL), "/"), os.Getenv(wickplugin.EnvPluginToken)
}

// CallbackRequest builds a request to wick's REST path (e.g.
// "/x/-/api/whoami") carrying the callback token.
func (e *Env) CallbackRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	base, tok := e.Callback()
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, err
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

// BaseURL rebuilds the public URL of the service from a proxied request
// (X-Forwarded-Proto/Host + X-Wick-Base), e.g. for an A2A agent card.
func BaseURL(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return proto + "://" + host + strings.TrimRight(r.Header.Get(wickplugin.HeaderBase), "/")
}
