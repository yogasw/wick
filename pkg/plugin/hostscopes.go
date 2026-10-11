package plugin

import (
	"fmt"
	"net/http"
	"strings"
)

// Host API scopes a plugin declares in host_scopes. The names are part of the
// wire contract (wick.host.v1): add, never rename.
//
// KEEP IN SYNC: adding or changing a scope, method/op, resource kind or admin
// label here MUST also update docs/plugins/host-api.md and the built-in
// skills internal/agents/skillsync/builtin/wick-plugin-authoring/SKILL.md and
// internal/agents/skillsync/builtin/wick-plugins/SKILL.md. A guard test fails
// when they drift.
const (
	ScopeHostWhoami           = "whoami"
	ScopeHostUsersRead        = "users.read"
	ScopeHostAccountsRead     = "accounts.read"
	ScopeHostSessionsRead     = "sessions.read"
	ScopeHostSessionsNotify   = "sessions.notify"
	ScopeHostTicketsRead      = "tickets.read"
	ScopeHostTicketsWrite     = "tickets.write"
	ScopeHostConfigReadOther  = "config.read_other"
	ScopeHostConfigReadSecret = "config.read_secret"
	ScopeHostConnectorsRead   = "connectors.read"
	ScopeHostConnectorsExec   = "connectors.execute"
	ScopeHostRestRead         = "rest.read"
	ScopeHostRestWrite        = "rest.write"
	ScopeHostDBRead           = "db.read"
	ScopeHostDBWrite          = "db.write"
	ScopeHostDatadir          = "datadir"
	ScopeHostNetListen        = "net.listen"
)

// Resource kinds a scope can require a host_resources declaration for.
//
// KEEP IN SYNC: adding or changing a scope, method/op, resource kind or admin
// label here MUST also update docs/plugins/host-api.md and the built-in
// skills internal/agents/skillsync/builtin/wick-plugin-authoring/SKILL.md and
// internal/agents/skillsync/builtin/wick-plugins/SKILL.md. A guard test fails
// when they drift.
const (
	HostResourceRest       = "rest"
	HostResourceDB         = "db"
	HostResourceConnectors = "connectors"
	HostResourceConfig     = "config"
)

// HostScope describes one Host API scope: what the admin page shows for it
// and how wick decides a call under it.
type HostScope struct {
	Name string
	// Label and Description are the admin card text ("what this plugin can
	// do"); every scope must have both (TestHostScopesHaveAdminLabel).
	Label       string
	Description string
	// DefaultAllow is the decision when no admin overlay row exists.
	DefaultAllow bool
	// Destructive marks scopes whose calls change data or reach people.
	Destructive bool
	// Sensitive scopes read data an admin should look at twice (secrets);
	// the admin page highlights them even when they are not destructive.
	Sensitive bool
	// Declarative scopes have no RPC; they only describe the plugin.
	Declarative bool
	// Resource names the host_resources block every call must match ("" =
	// none). A plugin without that declaration gets no access at all.
	Resource string
}

// HostScopes is the one table of official scopes, in admin display order.
//
// KEEP IN SYNC: adding or changing a scope, method/op, resource kind or admin
// label here MUST also update docs/plugins/host-api.md and the built-in
// skills internal/agents/skillsync/builtin/wick-plugin-authoring/SKILL.md and
// internal/agents/skillsync/builtin/wick-plugins/SKILL.md. A guard test fails
// when they drift.
var HostScopes = []HostScope{
	{Name: ScopeHostWhoami, Label: "Identify itself", Description: "Ask wick which plugin it is and what it may call.", DefaultAllow: true},
	{Name: ScopeHostUsersRead, Label: "Read users", Description: "List wick users and look one up by id or email (name, email, approval, admin flag, tags).", DefaultAllow: true},
	{Name: ScopeHostAccountsRead, Label: "Read connected accounts", Description: "List the accounts linked to a connector instance.", DefaultAllow: true},
	{Name: ScopeHostSessionsRead, Label: "Read session details", Description: "List sessions and read their metadata (never the conversation itself), as the acting user.", DefaultAllow: true},
	{Name: ScopeHostSessionsNotify, Label: "Message sessions", Description: "Send a message into a session, as the acting user.", DefaultAllow: true, Destructive: true},
	{Name: ScopeHostTicketsRead, Label: "Read tickets", Description: "List and read tickets, as the acting user.", DefaultAllow: true},
	{Name: ScopeHostTicketsWrite, Label: "Change tickets", Description: "Create and update tickets and attach sessions to them, as the acting user.", DefaultAllow: true, Destructive: true},
	{Name: ScopeHostConfigReadOther, Label: "Read other configs", Description: "Read configuration values owned by something other than this plugin (secret values stay hidden).", DefaultAllow: true},
	{Name: ScopeHostConfigReadSecret, Label: "Read other secrets", Description: "Read the declared secret config values of other owners (needs config.read_other too).", Sensitive: true, Resource: HostResourceConfig},
	{Name: ScopeHostConnectorsRead, Label: "List connector instances", Description: "List the connector instances the acting user can see.", DefaultAllow: true},
	{Name: ScopeHostConnectorsExec, Label: "Run connector operations", Description: "Run the declared connector operations as the acting user.", DefaultAllow: true, Resource: HostResourceConnectors},
	{Name: ScopeHostRestRead, Label: "Read wick API", Description: "Call the declared GET paths of wick's REST API as the acting user.", DefaultAllow: true, Resource: HostResourceRest},
	{Name: ScopeHostRestWrite, Label: "Change via wick API", Description: "Call the declared POST/PUT/PATCH/DELETE paths of wick's REST API as the acting user.", Destructive: true, Resource: HostResourceRest},
	{Name: ScopeHostDBRead, Label: "Read wick database", Description: "Run read-only SQL against the declared tables.", Resource: HostResourceDB},
	{Name: ScopeHostDBWrite, Label: "Write wick database", Description: "Run SQL that changes the declared tables.", Destructive: true, Resource: HostResourceDB},
	{Name: ScopeHostDatadir, Label: "Keep a data folder", Description: "Store files in its own persistent data folder.", DefaultAllow: true, Declarative: true},
	{Name: ScopeHostNetListen, Label: "Open a local port", Description: "Listen on a local network port.", DefaultAllow: true, Declarative: true},
}

// LookupHostScope returns the official scope named name.
func LookupHostScope(name string) (HostScope, bool) {
	for _, s := range HostScopes {
		if s.Name == name {
			return s, true
		}
	}
	return HostScope{}, false
}

// HostMethod is one Host RPC as ListMethods reports it.
type HostMethod struct {
	Name        string
	Scope       string
	Summary     string
	Destructive bool
	// UserLevel RPCs touch data owned by a person and always run as one
	// (the caller, else the plugin's wick_pat); plugin-level ones need none.
	UserLevel bool
}

// HostMethods lists every Host RPC in contract order. Rest and Query pick
// their scope per call (rest.read/rest.write, db.read/db.write); the entry
// names the read scope and the write half is destructive.
//
// KEEP IN SYNC: adding or changing a scope, method/op, resource kind or admin
// label here MUST also update docs/plugins/host-api.md and the built-in
// skills internal/agents/skillsync/builtin/wick-plugin-authoring/SKILL.md and
// internal/agents/skillsync/builtin/wick-plugins/SKILL.md. A guard test fails
// when they drift.
var HostMethods = []HostMethod{
	{Name: "Whoami", Scope: ScopeHostWhoami, Summary: "Who this plugin is and which scopes it holds."},
	{Name: "ListMethods", Scope: ScopeHostWhoami, Summary: "The Host API methods and their scopes."},
	{Name: "ListUsers", Scope: ScopeHostUsersRead, Summary: "List wick users."},
	{Name: "GetUser", Scope: ScopeHostUsersRead, Summary: "Get a user by id."},
	{Name: "GetUserByEmail", Scope: ScopeHostUsersRead, Summary: "Get a user by email."},
	{Name: "ConnectedAccounts", Scope: ScopeHostAccountsRead, Summary: "Accounts linked to a connector instance."},
	{Name: "ListSessions", Scope: ScopeHostSessionsRead, Summary: "List session metadata.", UserLevel: true},
	{Name: "GetSessionMeta", Scope: ScopeHostSessionsRead, Summary: "Get one session's metadata.", UserLevel: true},
	{Name: "SendToSession", Scope: ScopeHostSessionsNotify, Summary: "Send a message into a session.", Destructive: true, UserLevel: true},
	{Name: "GetTicket", Scope: ScopeHostTicketsRead, Summary: "Get a ticket.", UserLevel: true},
	{Name: "ListTickets", Scope: ScopeHostTicketsRead, Summary: "List a project's tickets.", UserLevel: true},
	{Name: "CreateTicket", Scope: ScopeHostTicketsWrite, Summary: "Create a ticket.", Destructive: true, UserLevel: true},
	{Name: "UpdateTicket", Scope: ScopeHostTicketsWrite, Summary: "Update a ticket.", Destructive: true, UserLevel: true},
	{Name: "AttachSession", Scope: ScopeHostTicketsWrite, Summary: "Attach a session to a ticket.", Destructive: true, UserLevel: true},
	{Name: "GetConfig", Scope: ScopeHostConfigReadOther, Summary: "Read a config value (another owner's needs config.read_other; its secrets config.read_secret)."},
	{Name: "ListConnectorInstances", Scope: ScopeHostConnectorsRead, Summary: "List connector instances.", UserLevel: true},
	{Name: "ExecuteConnector", Scope: ScopeHostConnectorsExec, Summary: "Run a declared connector operation.", UserLevel: true},
	{Name: "Invoke", Scope: "", Summary: "Call a method by name; uses that method's scope."},
	{Name: "Rest", Scope: ScopeHostRestRead, Summary: "Call wick's REST API (non-GET needs rest.write).", Destructive: true, UserLevel: true},
	{Name: "Query", Scope: ScopeHostDBRead, Summary: "Run SQL (write=true needs db.write).", Destructive: true, UserLevel: true},
}

// LookupHostMethod returns the Host RPC named name.
func LookupHostMethod(name string) (HostMethod, bool) {
	for _, m := range HostMethods {
		if m.Name == name {
			return m, true
		}
	}
	return HostMethod{}, false
}

// HostResources is what a plugin names in detail for the scopes that need it
// (rest.*, db.*, connectors.execute, config.read_secret): no declaration, no
// access.
//
// KEEP IN SYNC: adding or changing a scope, method/op, resource kind or admin
// label here MUST also update docs/plugins/host-api.md and the built-in
// skills internal/agents/skillsync/builtin/wick-plugin-authoring/SKILL.md and
// internal/agents/skillsync/builtin/wick-plugins/SKILL.md. A guard test fails
// when they drift.
type HostResources struct {
	Rest       []HostRestResource      `json:"rest,omitempty"`
	DB         []HostDBResource        `json:"db,omitempty"`
	Connectors []HostConnectorResource `json:"connectors,omitempty"`
	Config     []HostConfigResource    `json:"config,omitempty"`
}

// HostRestResource is one wick REST pattern: "*" matches one path segment,
// "**" the rest of the path.
type HostRestResource struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// HostDBResource is one table and the access mode ("read" or "write").
type HostDBResource struct {
	Table string `json:"table"`
	Mode  string `json:"mode"`
}

// HostConfigResource is one config value of another owner whose secret the
// plugin may read under config.read_secret.
type HostConfigResource struct {
	Owner string `json:"owner"`
	Key   string `json:"key"`
}

// HostConnectorResource is one connector module key and the ops it may run.
type HostConnectorResource struct {
	Connector string   `json:"connector"`
	Ops       []string `json:"ops"`
}

// Empty reports whether nothing is declared.
func (r *HostResources) Empty() bool {
	return r == nil || (len(r.Rest) == 0 && len(r.DB) == 0 && len(r.Connectors) == 0 && len(r.Config) == 0)
}

// Items lists the declared items of resource kind as the admin overlay keys
// them: "GET /api/x" for rest, the table name for db, "connector.op" for
// connectors, "owner/key" for config.
func (r *HostResources) Items(kind string) []string {
	if r == nil {
		return nil
	}
	var out []string
	switch kind {
	case HostResourceRest:
		for _, x := range r.Rest {
			out = append(out, strings.ToUpper(strings.TrimSpace(x.Method))+" "+x.Path)
		}
	case HostResourceDB:
		for _, x := range r.DB {
			out = append(out, x.Table)
		}
	case HostResourceConnectors:
		for _, x := range r.Connectors {
			for _, op := range x.Ops {
				out = append(out, x.Connector+"."+op)
			}
		}
	case HostResourceConfig:
		for _, x := range r.Config {
			out = append(out, ConfigItem(x.Owner, x.Key))
		}
	}
	return out
}

var restMethods = map[string]bool{
	http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true,
}

// ConfigItem is the host_resources.config item key of owner's key.
func ConfigItem(owner, key string) string { return owner + "/" + key }

// UnknownHostScopes lists host_scopes this wick does not know. They do not
// fail the manifest (a plugin built for a newer wick must still load, §8):
// the loader logs them and every call under them is denied.
func (m Manifest) UnknownHostScopes() []string {
	var out []string
	for _, s := range m.HostScopes {
		if _, ok := LookupHostScope(s); !ok {
			out = append(out, s)
		}
	}
	return out
}

// ValidateHostAccess checks host_scopes and host_resources: every scope is
// listed once, every rest/db/connectors/config scope comes with a matching
// declaration, and every declaration is well formed (REST paths only under
// /api/ or /manager/api/, no dot segments). Unknown scopes are not an error
// (see UnknownHostScopes).
func (m Manifest) ValidateHostAccess() error {
	seen := map[string]bool{}
	for _, s := range m.HostScopes {
		if seen[s] {
			return fmt.Errorf("host_scopes: scope %q listed twice", s)
		}
		seen[s] = true
	}
	res := m.HostResources
	if res == nil {
		res = &HostResources{}
	}
	var hasRestRead, hasRestWrite, hasDBRead, hasDBWrite bool
	for _, x := range res.Rest {
		method := strings.ToUpper(strings.TrimSpace(x.Method))
		if !restMethods[method] {
			return fmt.Errorf("host_resources.rest: invalid method %q", x.Method)
		}
		if err := validateRestPath(x.Path); err != nil {
			return fmt.Errorf("host_resources.rest %s: %w", x.Path, err)
		}
		if method == http.MethodGet {
			hasRestRead = true
		} else {
			hasRestWrite = true
		}
	}
	for _, x := range res.DB {
		if strings.TrimSpace(x.Table) == "" {
			return fmt.Errorf("host_resources.db: table is empty")
		}
		switch x.Mode {
		case "read":
			hasDBRead = true
		case "write":
			hasDBRead, hasDBWrite = true, true
		default:
			return fmt.Errorf("host_resources.db %s: mode must be read or write, got %q", x.Table, x.Mode)
		}
	}
	for _, x := range res.Connectors {
		if strings.TrimSpace(x.Connector) == "" {
			return fmt.Errorf("host_resources.connectors: connector is empty")
		}
		if len(x.Ops) == 0 {
			return fmt.Errorf("host_resources.connectors %s: ops is empty", x.Connector)
		}
	}
	for _, x := range res.Config {
		if strings.TrimSpace(x.Owner) == "" || strings.TrimSpace(x.Key) == "" {
			return fmt.Errorf("host_resources.config: owner and key are required")
		}
	}
	need := map[string]bool{
		ScopeHostConfigReadSecret: len(res.Config) > 0,
		ScopeHostRestRead:         hasRestRead,
		ScopeHostRestWrite:        hasRestWrite,
		ScopeHostDBRead:           hasDBRead,
		ScopeHostDBWrite:          hasDBWrite,
		ScopeHostConnectorsExec:   len(res.Connectors) > 0,
	}
	for _, s := range m.HostScopes {
		if ok, gated := need[s]; gated && !ok {
			sc, _ := LookupHostScope(s)
			return fmt.Errorf("host_scopes: %s needs a matching host_resources.%s declaration", s, sc.Resource)
		}
	}
	return nil
}

func validateRestPath(p string) error {
	if !strings.HasPrefix(p, "/api/") && !strings.HasPrefix(p, "/manager/api/") {
		return fmt.Errorf("path must start with /api/ or /manager/api/")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("path must not contain dot segments")
		}
	}
	return nil
}
