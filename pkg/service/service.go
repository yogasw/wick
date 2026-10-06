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
	"time"

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
	// RemoteConfigs are per-agent fields of a RemoteSource (e.g.
	// entity.StructToConfigs(RemoteConfig{})): filled per remote agent in
	// the wizard and handed to Send as RemoteTurn.Config. The plugin decides
	// the fallback when one is empty, usually to its own Configs.
	RemoteConfigs []entity.Config
	// CallbackScopes are the wick REST scopes the plugin's callback token
	// may use (see Env.Callback).
	CallbackScopes []string
	// Register mounts the HTTP routes on mux (Go 1.22 patterns).
	Register func(mux *http.ServeMux, env *Env)
	// RemoteSource, when set, makes the plugin selectable as a Team remote
	// agent source ("Plugin" in the Remote agent wizard).
	RemoteSource RemoteSource
	// AutoOff declares whether wick may stop the service while it is idle
	// and wake it on the next request. The zero value never auto-offs.
	AutoOff AutoOff
}

// AutoOff is a service's own say on being stopped while idle. wick counts
// a service idle when no request to /x/{key}/* or the remote RPC is in
// flight and no remote turn is open; after the idle limit it stops the
// process (state "sleeping") and starts it again on the next request, which
// waits for the boot. It is only the default: the wick admin can force
// auto-off on or off per service.
//
// Set Supported only when nothing has to run without a request coming in:
// no background worker, poller, scheduler or outbound listener, and any
// state worth keeping is persisted (files, wick). Webhooks are fine, a
// webhook request wakes the service — but a sender with a very tight
// timeout (a few seconds) may time out during a cold start.
type AutoOff struct {
	Supported bool
	// Reason tells the admin why the service cannot auto-off, e.g. "polls
	// the job queue in the background". Shown when Supported is false.
	Reason string
	// DefaultIdle is the idle limit; 0 = 15 minutes.
	DefaultIdle time.Duration
}

// RemoteTurn, RemoteEvent and RemoteSendResult are the wire types of the
// remote_source RPC (event schema v1).
type (
	RemoteTurn       = wickplugin.RemoteTurn
	RemoteEvent      = wickplugin.RemoteEvent
	RemoteSendResult = wickplugin.RemoteSendResult
	SessionField     = wickplugin.SessionField
)

// RemoteSource is what a service plugin implements to act as a Team remote
// agent: Send a turn, Receive its events (closed after a terminal done/error
// event), Done to release what Send set up.
type RemoteSource interface {
	Send(ctx context.Context, turn RemoteTurn) (RemoteSendResult, error)
	Receive(ctx context.Context, handle string) (<-chan RemoteEvent, error)
	Done(handle string)
}

// RemoteInjector is optionally implemented by a RemoteSource whose remote
// takes a message while a turn still runs: Inject hands text to handle's
// turn, and the answer flows on that turn's Receive stream. Without it wick
// queues the message as the next turn.
type RemoteInjector interface {
	Inject(ctx context.Context, handle, text string) error
}

// RemoteCanceler is optionally implemented by a RemoteSource whose remote
// can stop a running turn on its own side: Cancel is called when the turn is
// stopped in wick. Without it a stop only ends wick's listening; the remote
// may keep working.
type RemoteCanceler interface {
	Cancel(ctx context.Context, handle string) error
}

// RemoteSessionFielder is optionally implemented by a RemoteSource whose
// new sessions take values before the first message (e.g. a repository and
// a branch). SessionFields gets the agent's RemoteConfigs values (agentCfg)
// so defaults can fall back from the agent's config to the plugin's own
// Configs. The answers arrive on the first RemoteTurn.Options; report the
// values really used on RemoteSendResult.Options.
type RemoteSessionFielder interface {
	SessionFields(agentCfg map[string]string) []SessionField
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
