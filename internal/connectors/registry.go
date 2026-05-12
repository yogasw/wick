// Package connectors is the central registry for every connector
// definition wick will expose via MCP. Downstream apps append to it via
// app.RegisterConnector; the MCP and admin-UI layers walk All() at boot
// to validate definitions and seed default instances.
//
// Shape of a connector module (see internal/docs/connectors-design.md
// for the full design):
//
//  1. Package under internal/connectors/<name>/ exposing a Meta builder,
//     a typed Creds struct (`wick:"..."` tags), a typed Input struct,
//     and an `Execute(c *connector.Ctx) (any, error)` function.
//  2. Register here inside RegisterBuiltins() (default-on for every
//     wick app — github, httprest) or RegisterLabSamples() (cmd/lab
//     only — crudcrud), or in the downstream project's main.go via
//     app.RegisterConnector.
//
// Connector definitions live in code; per-instance rows (credentials,
// labels, tags) live in the connector_instances table — populated by
// the admin UI in a later phase.
package connectors

import (
	"github.com/yogasw/wick/internal/connectors/crudcrud"
	"github.com/yogasw/wick/internal/connectors/github"
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

// Register appends a fully-resolved Module record to the registry.
// Called from app.RegisterConnector; do not call directly from app code.
//
// Idempotent on Meta.Key: re-registering the same key REPLACES the
// existing entry. This keeps server stop→start safe — wickmanager is
// registered mid-boot with runtime Deps (configsSvc, jobsSvc, ...) that
// are rebuilt on each boot. A plain append would trip Bootstrap's
// duplicate-key check; a skip would leave handlers wired to stale
// services from the previous boot.
func Register(m connector.Module) {
	for i, existing := range extra {
		if existing.Meta.Key == m.Meta.Key {
			extra[i] = m
			return
		}
	}
	extra = append(extra, m)
}

// RegisterBuiltins seeds in-house connectors every downstream wick app
// gets by default — the public-API connectors (github, httprest) that
// most apps want available immediately. Called from
// internal/pkg/api/server.go at boot, before connectors.All().
//
// Idempotent on Meta.Key: re-calling appends nothing if the key was
// already registered.
//
// Note: wickmanager is registered inline in server.go (line ~494)
// because it requires runtime Deps (configsSvc, jobsSvc, etc.) that
// only exist mid-boot.
func RegisterBuiltins() {
	registerOnce(connector.Module{
		Meta:       withConnectorTag(github.Meta()),
		Configs:    entity.StructToConfigs(github.Configs{}),
		Operations: github.Operations(),
	})
	registerOnce(connector.Module{
		Meta:       withConnectorTag(httprest.Meta()),
		Configs:    entity.StructToConfigs(httprest.Configs{}),
		Operations: httprest.Operations(),
	})
	registerOnce(connector.Module{
		Meta:        withConnectorTag(slack.Meta()),
		Configs:     entity.StructToConfigs(slack.Configs{}),
		Operations:  slack.Operations(),
		HealthCheck: slack.HealthCheck,
	})
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
func registerOnce(m connector.Module) {
	for _, existing := range extra {
		if existing.Meta.Key == m.Meta.Key {
			return
		}
	}
	extra = append(extra, m)
}

// All returns every registered connector definition in registration
// order.
func All() []connector.Module {
	return extra
}
