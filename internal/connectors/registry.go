// Package connectors is the central registry for every connector
// definition wick will expose via MCP. Downstream apps append to it via
// app.RegisterConnector; the MCP and admin-UI layers walk All() at boot
// to validate definitions and seed default instances.
//
// Shape of a connector module (see internal/planning/archive/connectors-design.md
// for the full design):
//
//  1. Package under internal/connectors/<name>/ exposing a Meta builder,
//     a typed Creds struct (`wick:"..."` tags), a typed Input struct,
//     and an `Execute(c *connector.Ctx) (any, error)` function.
//  2. Register here inside RegisterBuiltins() (default-on for every
//     wick app — httprest, slack) or RegisterLabSamples() (cmd/lab
//     only — crudcrud), or in the downstream project's main.go via
//     app.RegisterConnector. (Connectors shipped as external plugins —
//     github, bitbucket, google_workspace — live under plugins/connector/
//     and are NOT registered here.)
//
// Connector definitions live in code; per-instance rows (credentials,
// labels, tags) live in the connector_instances table — populated by
// the admin UI in a later phase.
package connectors

import (
	"github.com/yogasw/wick/internal/connectors/crudcrud"
	"github.com/yogasw/wick/internal/connectors/httprest"
	"github.com/yogasw/wick/internal/connectors/slack"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// withConnectorTag is a small helper that appends the shared Connector
// group tag to a Meta's DefaultTags. Used by RegisterBuiltins and
// RegisterLabSamples so every wick-shipped connector lands under the
// "Connector" group on the home page without each module having to
// import the tags package itself.
func withConnectorTag(m connector.Meta, extra ...tool.DefaultTag) connector.Meta {
	m.DefaultTags = append([]tool.DefaultTag{tags.Connector}, extra...)
	return m
}

// extra holds connector definitions registered by downstream projects,
// plus the modules added by RegisterBuiltins / RegisterLabSamples.
// All() returns this slice verbatim.
var extra []connector.Module

// listeners are notified for every Register call (including replacements)
// AND replayed for every module already in `extra` at the time the
// listener was added via OnRegister. Used by the workflow connector
// registry to stay in sync without depending on boot ordering — see
// internal/agents/workflow/setup/connectors.go.
var listeners []func(connector.Module)

// Register appends a fully-resolved Module record to the registry.
// Called from app.RegisterConnector; do not call directly from app code.
//
// Idempotent on Meta.Key: re-registering the same key REPLACES the
// existing entry. This keeps server stop→start safe — wickmanager is
// registered mid-boot with runtime Deps (configsSvc, jobsSvc, ...) that
// are rebuilt on each boot. A plain append would trip Bootstrap's
// duplicate-key check; a skip would leave handlers wired to stale
// services from the previous boot.
//
// After the (append|replace), every listener registered via OnRegister
// is notified with the resolved module. Listeners run synchronously on
// the calling goroutine — keep them cheap. Registrations all happen on
// the main boot goroutine today; revisit if that ever changes.
func Register(m connector.Module) {
	for i, existing := range extra {
		if existing.Meta.Key == m.Meta.Key {
			extra[i] = m
			notify(m)
			return
		}
	}
	extra = append(extra, m)
	notify(m)
}

// OnRegister installs a listener that fires for every connector module
// in the registry — once per module already present at the time of the
// call (catch-up), then once per future Register call (catch future).
// The catch-up loop matters: workflow setup runs after some builtins
// have already been registered, and a future-only subscription would
// silently drop them.
//
// Listeners are not removable. The registry lives for the lifetime of
// the process; subscriptions are intended for setup-time wiring, not
// dynamic plug-in/plug-out.
func OnRegister(fn func(connector.Module)) {
	for _, m := range extra {
		fn(m)
	}
	listeners = append(listeners, fn)
}

func notify(m connector.Module) {
	for _, fn := range listeners {
		fn(m)
	}
}

// builtinModules returns the in-house connector modules every "full"
// wick build registers. Extracted from RegisterBuiltins so profile
// selection (profileModules) can filter the same list by Meta.Key
// without duplicating the definitions.
func builtinModules() []connector.Module {
	return []connector.Module{
		{
			Meta:               withConnectorTag(httprest.Meta(), tags.API),
			Configs:            entity.StructToConfigs(httprest.Configs{}),
			Operations:         httprest.Operations(),
			AllowSessionConfig: true,
		},
		{
			Meta:        withConnectorTag(slack.Meta(), tags.Communication),
			Configs:     entity.StructToConfigs(slack.Configs{}),
			Operations:  slack.Operations(),
			HealthCheck: slack.HealthCheck,
			OAuth:       slack.SlackOAuthMeta(),
		},
		// loki and phoenix moved out-of-tree to downloadable plugins
		// (plugins/connector/loki, plugins/connector/phoenix). They are no
		// longer compiled into the binary; install via
		// `<app> plugin install loki` / `<app> plugin install phoenix`.
		//
		// google_workspace moved out-of-tree to a downloadable plugin
		// (plugins/connector/google_workspace). It is no longer compiled
		// into the binary; install it via `<app> plugin install google_workspace`.
	}
}

// Build profiles select which builtin connectors a binary registers.
// The active profile is read at boot from the configs DB row
// (configs.KeyProfile) via configsSvc.Profile(). "full" (default and
// any unknown value) preserves the historical all-connectors behaviour.
const (
	ProfileFull  = "full"
	ProfileAgent = "agent"
	ProfileLite  = "lite"
)

// agentConnectors is the curated allow-list for the "agent" profile.
// Widen or narrow it with a one-line edit here. Only builtins can appear
// here (the allow-list filters builtinModules); github moved to a plugin
// so it's no longer eligible — install it via `<app> plugin install github`.
func agentConnectors() map[string]bool {
	return map[string]bool{
		httprest.Meta().Key: true,
		slack.Meta().Key:    true,
	}
}

// profileModules is the pure selector behind RegisterProfile: given a
// profile name it returns the builtin modules that profile should
// register, without touching global registry state (so it is trivially
// unit-testable).
func profileModules(profile string) []connector.Module {
	switch profile {
	case ProfileLite:
		return nil
	case ProfileAgent:
		allow := agentConnectors()
		out := make([]connector.Module, 0, len(allow))
		for _, m := range builtinModules() {
			if allow[m.Meta.Key] {
				out = append(out, m)
			}
		}
		return out
	default: // ProfileFull and any unknown value
		return builtinModules()
	}
}

// RegisterProfile seeds the builtin connectors permitted by the named
// profile. Idempotent on Meta.Key via registerOnce.
func RegisterProfile(profile string) {
	for _, m := range profileModules(profile) {
		registerOnce(m)
	}
}

// RegisterBuiltins seeds in-house connectors every downstream wick app
// gets by default. Idempotent on Meta.Key via registerOnce.
func RegisterBuiltins() {
	for _, m := range builtinModules() {
		registerOnce(m)
	}
}

// RegisterLabSamples seeds the demo-only connectors shipped with the
// cmd/lab binary — currently the crudcrud sample. Downstream wick apps
// do not call this; they register their own connectors via main.go.
func RegisterLabSamples() {
	registerOnce(connector.Module{
		Meta:       withConnectorTag(crudcrud.Meta()),
		Configs:    entity.StructToConfigs(crudcrud.Configs{}),
		Operations: crudcrud.Operations(),
	})
}

// registerOnce is the internal de-dupe helper for the seed paths.
// Notifies listeners on first-time add so OnRegister callbacks stay in
// sync regardless of whether RegisterBuiltins fires before or after
// the subscriber attaches.
func registerOnce(m connector.Module) {
	for _, existing := range extra {
		if existing.Meta.Key == m.Meta.Key {
			return
		}
	}
	extra = append(extra, m)
	notify(m)
}

// All returns every registered connector definition in registration
// order.
func All() []connector.Module {
	return extra
}
