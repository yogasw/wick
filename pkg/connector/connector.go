// Package connector defines the public contract every connector module
// must satisfy. Connectors are the third class of wick module — sibling
// to Tools (human, web UI) and Jobs (scheduler, background) — built
// specifically to be consumed by LLM clients via MCP.
//
// One connector definition is a Go module that wraps a single external
// API. A definition exposes one shared credential set (URL, token, ...)
// and N Operations — small, named actions an LLM can invoke. Each
// operation has its own input schema and Execute function.
//
// At runtime an admin or user creates N instances of a definition
// through the web UI; each instance carries its own credential values
// and tag-based access control. Per instance, every operation can be
// toggled on or off, so admins can disable destructive or unverified
// operations without giving up the rest of the connector.
//
// Connectors are not surfaced to MCP clients as N×M static tools. The
// MCP layer exposes a fixed three-tool meta surface (wick_list /
// wick_search / wick_execute), and the LLM discovers individual
// (instance, operation) pairs at runtime via wick_list. Each pair is
// addressed by an opaque tool_id of the form "conn:{connector_id}/
// {op_key}", which wick_execute resolves back to a single ExecuteFunc
// call. Adding or removing instances therefore never changes the
// client's cached tool list — no manual "Refresh tool list" needed.
//
// A typical downstream registration looks like:
//
//	package main
//
//	import (
//	    "github.com/yogasw/wick/app"
//	    "myproject/connectors/loki"
//	)
//
//	func main() {
//	    app.RegisterConnector(loki.Meta(), loki.Creds{}, loki.Operations())
//	    app.Run()
//	}
//
// Wick reflects the typed Creds struct and each operation's typed Input
// struct into entity.Configs rows (via `wick:"..."` tags), so both the
// admin form for a new instance and the per-operation MCP JSON Schema
// can be auto-generated.
package connector

import (
	"context"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// Meta is the static metadata for a connector definition. Key must be
// a unique slug across every connector; entity.Connector.Key references
// it (one Meta.Key, many entity rows for multi-instance setups).
//
// Description is shown to the admin in the manager UI. The LLM never
// sees it directly — it only reads per-operation Description fields
// surfaced through wick_list / wick_search.
type Meta struct {
	Key         string
	Name        string
	Description string
	Icon        string
	// Fixed marks this connector as single-instance. Wick auto-seeds one
	// row on first boot, the admin UI hides the "Add new instance"
	// button, and connectors.Repo.Create rejects a second insert with
	// ErrFixedInstanceViolation. Useful when the connector wraps a
	// single in-process resource (e.g. wickmanager) or an external
	// service that can only have one configuration.
	//
	// Default false = many instances allowed (existing behaviour).
	Fixed bool
	// DefaultTags is the list of tags wick auto-attaches to each newly
	// seeded row for this connector at boot. Tags are reused across
	// connectors via the central tags package; admins can add or remove
	// links from the admin UI without redeploy. Existing rows are
	// untouched on subsequent boots — admin unlinks survive restarts.
	//
	// Convention: every connector should set at least `tags.Connector`
	// so the home page groups it under "Connector". Add module-specific
	// tags (e.g. `tags.System` for built-in maintenance connectors) on
	// top of that.
	DefaultTags []tool.DefaultTag
}

// ExecuteFunc is the per-operation handler signature. It receives a
// *Ctx carrying the resolved per-instance credential map, the per-call
// input arguments from the LLM, and a configured *http.Client. The
// returned value is JSON-marshaled into the MCP tools/call result —
// return a typed struct or slice for a stable, ramping shape rather
// than the raw upstream payload.
type ExecuteFunc func(c *Ctx) (any, error)

// Operation is one named action exposed by a connector definition. A
// single connector can carry many operations: a "github" connector
// might have list_repos, create_issue, list_issues, add_comment.
//
// Description is the load-bearing field for the LLM — it is shown
// verbatim in the wick_list / wick_search payload and is the primary
// signal the model uses to decide whether to call this op. Use action
// verbs and be specific ("List repositories visible to the
// authenticated user", not "list").
//
// Destructive marks operations that mutate state in a way that is
// hard or impossible to undo (delete, force-push, send message, post
// comment). Wick uses this hint to default the per-instance toggle to
// off so admins must explicitly opt in, and to surface a warning chip
// in the admin UI.
type Operation struct {
	Key         string
	Name        string
	Description string
	Input       []entity.Config
	Execute     ExecuteFunc
	Destructive bool
	// Docs carries the opt-in self-documentation fields the workflow
	// MCP `workflow_node_detail` op surfaces to AI clients (examples,
	// quirks, templateable fields, pair-with, common pitfalls).
	// Zero-value Docs = current behaviour; populate per op when worth
	// it. See pkg/wickdocs + internal/docs/workflow/24-describe-contract.md.
	wickdocs.Docs
}

// Op is a small constructor that reflects a typed input struct into
// the Operation's Input rows. Equivalent to building Operation{} by
// hand and calling entity.StructToConfigs(input) yourself, but reads
// nicer when listing many operations inline.
//
// docs carries the optional self-documentation bundle exposed by the
// workflow MCP `workflow_node_detail` op — quirks, examples, sample
// payloads, pair-with, common pitfalls. Pass `wickdocs.Docs{}` when
// the op has no extra guidance; the description + reflected schema
// alone are already enough for callers in that case.
//
//	connector.Op("query", "Query Logs",
//	    "Search Loki using LogQL.",
//	    QueryInput{}, queryExec, wickdocs.Docs{})
//
// Pass struct{}{} as input when the operation takes no input arguments.
func Op[I any](key, name, description string, input I, exec ExecuteFunc, docs wickdocs.Docs) Operation {
	return Operation{
		Key:         key,
		Name:        name,
		Description: description,
		Input:       entity.StructToConfigs(input),
		Execute:     exec,
		Docs:        docs,
	}
}

// OpDestructive is the destructive-marked variant of Op. The resulting
// Operation defaults to disabled when a new instance is created, and
// the admin UI flags it so admins know to verify before enabling.
func OpDestructive[I any](key, name, description string, input I, exec ExecuteFunc, docs wickdocs.Docs) Operation {
	op := Op(key, name, description, input, exec, docs)
	op.Destructive = true
	return op
}

// OpHealth is one entry in the report returned by Module.HealthCheck.
// OK=true means the configured credential has every upstream permission
// the operation needs; OK=false means the op should be system-disabled
// and Reason explains what is missing (e.g. "needs scope: chat:write").
//
// HealthCheck implementations should return one OpHealth per operation
// the module exposes that can be permission-checked; ops omitted from
// the report are left untouched (neither system-disabled nor cleared).
type OpHealth struct {
	Key    string
	OK     bool
	Reason string
}

// HealthCheckFunc verifies that the configured credentials carry the
// upstream permissions every operation needs. The framework calls it
// from the admin UI's "Check Permissions" button. Implementations
// should make one cheap probe call (e.g. Slack's auth.test) and
// project the granted permissions against each operation's requirements.
type HealthCheckFunc func(c *Ctx) ([]OpHealth, error)

// OAuthMeta describes how a connector participates in the OAuth 2.0 flow.
// The generic manager handler uses AuthorizeURL and Scopes to build the
// consent redirect; GetUserIdentity is called after the token exchange to
// resolve who the token belongs to.
//
// Set Module.OAuth to a non-nil pointer to opt in. The manager UI will
// render an "OAuth App" section on the connector list page and a "Connect"
// button on user-token rows automatically.
type OAuthMeta struct {
	// AuthorizeURL is the OAuth consent redirect URL
	// (e.g. https://slack.com/oauth/v2/authorize).
	AuthorizeURL string
	// Scopes is the space- or comma-separated list of requested scopes
	// (sent as the user_scope param for Slack, scope for standard OAuth2).
	Scopes string
	// DisplayName is shown on the Connect button (e.g. "Slack", "Google").
	DisplayName string
	// Icon is an SVG string or emoji rendered next to the Connect button.
	Icon string
	// GetUserIdentity exchanges a fresh access token for a unique user ID
	// and human-readable display name. Called after the code→token exchange
	// to route the token to the correct connector row.
	GetUserIdentity func(ctx context.Context, accessToken string) (userID, displayName string, err error)
}

// Module is the internal, fully-resolved registration record wick keeps
// for every connector definition. It is produced by app.RegisterConnector
// — the Meta, the configs reflected from the typed Creds struct, and
// the list of operations the connector exposes. Downstream code does
// not construct Module directly.
//
// HealthCheck is optional. When non-nil, the connector detail page
// renders a "Check Permissions" button that invokes it and toggles
// per-operation system_disabled flags based on the report.
//
// OAuth is non-nil when this connector supports user OAuth. The manager UI
// shows an "OAuth App" section on the list page and a "Connect" button on
// detail pages automatically — no per-connector handler wiring needed.
type Module struct {
	Meta        Meta
	Configs     []entity.Config
	Operations  []Operation
	HealthCheck HealthCheckFunc
	// OAuth is non-nil when this connector supports user OAuth.
	OAuth *OAuthMeta
}
