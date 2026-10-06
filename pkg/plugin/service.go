package plugin

import (
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
)

// Service plugins reuse the tool plugin machinery: HTTP on the unix socket in
// EnvToolSocket plus the Tool control service (Schema/Configure/Health) over
// go-plugin gRPC. They differ in lifecycle (always-on, supervised) and in
// where the host mounts them: /x/{key}/*, with auth decided per route.

// Callback env the host sets on a service plugin so it can call wick's REST
// API back without holding any other credential (same idea as
// WICK_CLI_TOKEN). The token is scoped to the plugin and revocable; a plugin
// must never log it.
const (
	EnvPluginToken = "WICK_PLUGIN_TOKEN"
	EnvBaseURL     = "WICK_BASE_URL"
)

// Route auth modes of a service plugin.
const (
	AuthPublic  = "public"       // anyone
	AuthToken   = "token"        // Authorization: Bearer <admin-generated token>
	AuthSession = "wick-session" // a signed-in wick user
)

// ValidAuth reports whether a is a known route auth mode.
func ValidAuth(a string) bool { return a == AuthPublic || a == AuthToken || a == AuthSession }

// ServiceRoute is one path prefix (relative to /x/{key}) and its auth mode.
// The longest matching prefix wins; a path no route matches answers 404.
type ServiceRoute struct {
	Prefix string `json:"prefix"`
	Auth   string `json:"auth"`
}

// Capability names a service plugin can declare.
const (
	CapRemoteSource = "remote_source"
	// CapRemoteInject: the remote source also takes a message for a turn
	// still running (RemotePathInject).
	CapRemoteInject = "remote_inject"
	// CapRemoteCancel: the remote source can also stop a running turn on
	// the remote's side (RemotePathCancel).
	CapRemoteCancel = "remote_cancel"
	// CapRemoteSessionFields: the remote source declares new-session fields
	// (RemotePathSessionFields), e.g. a repository and a branch.
	CapRemoteSessionFields = "remote_session_fields"
)

// ServiceModule is the service half of a kind=service manifest.
type ServiceModule struct {
	Meta    ToolMeta        `json:"meta"`
	Routes  []ServiceRoute  `json:"routes"`
	Configs []entity.Config `json:"configs,omitempty"`
	// RemoteConfigs are the per-agent fields of a remote_source plugin:
	// filled when a remote agent is created or edited, stored per agent and
	// sent on every RemoteTurn.Config.
	RemoteConfigs []entity.Config `json:"remote_configs,omitempty"`
	// Capabilities lists extras the plugin implements, e.g. remote_source
	// (a Team remote agent source served on RemotePath*).
	Capabilities []string `json:"capabilities,omitempty"`
	// CallbackScopes are the wick REST scopes the plugin's WICK_PLUGIN_TOKEN
	// may use.
	CallbackScopes []string `json:"callback_scopes,omitempty"`
	// AutoOff is the plugin's own say on being stopped while idle; nil is
	// the same as Supported=false with no reason.
	AutoOff *ServiceAutoOff `json:"auto_off,omitempty"`
}

// DefaultAutoOffIdleSeconds is the idle limit of an auto-off service whose plugin
// sets none.
const DefaultAutoOffIdleSeconds = 15 * 60

// ServiceAutoOff is the wire form of service.AutoOff: whether the host may
// stop the process after it has been idle (no request in flight, no remote
// turn open) and start it again on the next request. It only sets the
// default; the wick admin can force auto-off on or off per service.
type ServiceAutoOff struct {
	Supported bool `json:"supported"`
	// Reason says why the plugin cannot auto-off (shown on the dashboard).
	Reason string `json:"reason,omitempty"`
	// DefaultIdleSeconds is the idle limit; 0 = DefaultAutoOffIdleSeconds.
	DefaultIdleSeconds int `json:"default_idle_seconds,omitempty"`
}

// Has reports whether the module declares capability c.
func (m ServiceModule) Has(c string) bool {
	for _, x := range m.Capabilities {
		if x == c {
			return true
		}
	}
	return false
}

// MatchRoute returns the route of the longest prefix matching path
// (relative to /x/{key}, always starting with "/").
func (m ServiceModule) MatchRoute(path string) (ServiceRoute, bool) {
	best, ok := ServiceRoute{}, false
	for _, r := range m.Routes {
		p := "/" + strings.Trim(r.Prefix, "/")
		if path == p || p == "/" || strings.HasPrefix(path, p+"/") {
			if !ok || len(p) > len("/"+strings.Trim(best.Prefix, "/")) {
				best, ok = r, true
			}
		}
	}
	return best, ok
}

// Internal paths a remote_source service plugin serves on its socket. The
// host never forwards a client path under RemotePrefix, so only wick reaches
// them.
const (
	RemotePrefix       = "/_wick/"
	RemotePathSend     = "/_wick/remote/send"     // POST RemoteTurn → {"handle":…}
	RemotePathEvents   = "/_wick/remote/events"   // GET ?handle= → SSE of RemoteEvent
	RemotePathDone     = "/_wick/remote/done"     // POST {"handle":…}
	RemotePathDescribe = "/_wick/remote/describe" // GET → {"name","detail","inject","cancel"}
	RemotePathInject   = "/_wick/remote/inject"   // POST RemoteInject → 204
	// RemotePathCancel stops the turn handle on the remote's side (Stop in
	// wick). Only sent when describe says "cancel": true.
	RemotePathCancel = "/_wick/remote/cancel" // POST {"handle":…} → 204
	// RemotePathSessionFields lists the fields a new session of an agent
	// asks for, defaults filled from that agent's config. Only asked when
	// describe says "session_fields": true.
	RemotePathSessionFields = "/_wick/remote/session-fields" // POST RemoteSessionFieldsRequest → {"fields":[…]}
)

// SessionField is one value a remote agent's new session asks for before
// its first message (e.g. a repository and a branch). wick renders whatever
// the plugin declares as editable chips in the new chat's composer, keeps
// the answers on the session and sends them on RemoteTurn.Options; once
// the remote session exists they are read-only.
type SessionField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	// Default is used when the user leaves the field empty, usually
	// resolved from the agent's config and then the plugin's.
	Default  string `json:"default,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// RemoteSessionFieldsRequest asks for an agent's new-session fields.
type RemoteSessionFieldsRequest struct {
	AgentID string            `json:"agent_id,omitempty"`
	Config  map[string]string `json:"config,omitempty"`
}

// RemoteInject is a message for the turn handle while it still runs: the
// plugin hands it to the remote and the running turn's stream carries the
// answer. Only sent when describe says "inject": true.
type RemoteInject struct {
	Handle string `json:"handle"`
	Text   string `json:"text"`
}

// RemoteTurn is one user message a Team remote agent sends to the plugin.
type RemoteTurn struct {
	Text      string `json:"text"`
	SessionID string `json:"session_id,omitempty"`
	// ContextID is the conversation the plugin handed back on a previous
	// turn of the same session ("" on the first).
	ContextID string `json:"context_id,omitempty"`
	// AgentID is the wick remote agent the turn belongs to.
	AgentID string `json:"agent_id,omitempty"`
	// Config is that agent's RemoteConfigs values in plaintext (secrets
	// decrypted; only ever sent over the plugin's local socket). An unset
	// key means the agent left it empty.
	Config map[string]string `json:"config,omitempty"`
	// Options are the session's SessionField answers by key, as chosen when
	// the session was created (an unset key = use the field's default).
	Options map[string]string `json:"options,omitempty"`
}

// RemoteSendResult is what RemotePathSend answers.
type RemoteSendResult struct {
	Handle    string `json:"handle"`
	ContextID string `json:"context_id,omitempty"`
	// Options are the SessionField values the remote session actually uses
	// (defaults resolved), shown read-only on the session afterwards.
	Options map[string]string `json:"options,omitempty"`
}

// RemoteEvent mirrors wick's remote event schema v1 field for field (kind is
// text_delta | text | status | attachment | done | error).
type RemoteEvent struct {
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Status string `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
	Name   string `json:"name,omitempty"`
	URL    string `json:"url,omitempty"`
	MIME   string `json:"mime,omitempty"`
	Note   string `json:"note,omitempty"`
}

// RemoteEventSchema is the event schema version RemoteEvent follows.
const RemoteEventSchema = 1

// BuildSelfServiceManifest is BuildSelfManifest for a service binary.
func BuildSelfServiceManifest(sm ServiceModule, signKeyPath string) (Manifest, error) {
	m, err := BuildSelfManifest(connector.Module{Meta: serviceConnectorMeta(sm.Meta)}, signKeyPath)
	if err != nil {
		return Manifest{}, err
	}
	m.Kind = KindService
	m.Service = &sm
	return m, nil
}

// serviceConnectorMeta is the in-memory Module.Meta mirror of a service's
// meta (not written to plugin.json; see Manifest.MarshalJSON).
func serviceConnectorMeta(tm ToolMeta) connector.Meta {
	return connector.Meta{
		Key:         tm.Key,
		Name:        tm.Name,
		Description: tm.Description,
		Icon:        tm.Icon,
	}
}
