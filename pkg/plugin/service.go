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
const CapRemoteSource = "remote_source"

// ServiceModule is the service half of a kind=service manifest.
type ServiceModule struct {
	Meta    ToolMeta        `json:"meta"`
	Routes  []ServiceRoute  `json:"routes"`
	Configs []entity.Config `json:"configs,omitempty"`
	// Capabilities lists extras the plugin implements, e.g. remote_source
	// (a Team remote agent source served on RemotePath*).
	Capabilities []string `json:"capabilities,omitempty"`
	// CallbackScopes are the wick REST scopes the plugin's WICK_PLUGIN_TOKEN
	// may use.
	CallbackScopes []string `json:"callback_scopes,omitempty"`
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
	RemotePathSend     = "/_wick/remote/send"    // POST RemoteTurn → {"handle":…}
	RemotePathEvents   = "/_wick/remote/events"  // GET ?handle= → SSE of RemoteEvent
	RemotePathDone     = "/_wick/remote/done"    // POST {"handle":…}
	RemotePathDescribe = "/_wick/remote/describe" // GET → {"name","detail"}
)

// RemoteTurn is one user message a Team remote agent sends to the plugin.
type RemoteTurn struct {
	Text      string `json:"text"`
	SessionID string `json:"session_id,omitempty"`
	// ContextID is the conversation the plugin handed back on a previous
	// turn of the same session ("" on the first).
	ContextID string `json:"context_id,omitempty"`
}

// RemoteSendResult is what RemotePathSend answers.
type RemoteSendResult struct {
	Handle    string `json:"handle"`
	ContextID string `json:"context_id,omitempty"`
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
	m, err := BuildSelfManifest(connector.Module{Meta: connector.Meta{
		Key:         sm.Meta.Key,
		Name:        sm.Meta.Name,
		Description: sm.Meta.Description,
		Icon:        sm.Meta.Icon,
	}}, signKeyPath)
	if err != nil {
		return Manifest{}, err
	}
	m.Kind = KindService
	m.Service = &sm
	return m, nil
}
