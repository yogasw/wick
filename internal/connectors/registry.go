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
//  2. Register here inside RegisterBuiltins() (core wick lab) or in the
//     downstream project's main.go via app.RegisterConnector.
//
// Connector definitions live in code; per-instance rows (credentials,
// labels, tags) live in the connector_instances table — populated by
// the admin UI in a later phase.
package connectors

import (
	"github.com/yogasw/wick/internal/connectors/crudcrud"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
)

// extra holds connector definitions registered by downstream projects
// (and, for the wick lab binary, by RegisterBuiltins). All() returns
// this slice verbatim — wick's own in-house connectors are opt-in.
var extra []connector.Module

// Register appends a fully-resolved Module record to the registry.
// Called from app.RegisterConnector; do not call directly from app code.
func Register(m connector.Module) {
	extra = append(extra, m)
}

// RegisterBuiltins appends wick's own in-house connectors to the
// registry. Intended for the wick lab binary (cmd/lab); downstream
// projects start with an empty registry and register only their own
// connectors.
func RegisterBuiltins() {
	extra = append(extra,
		connector.Module{
			Meta:       crudcrud.Meta(),
			Configs:    entity.StructToConfigs(crudcrud.Configs{}),
			Operations: crudcrud.Operations(),
		},
	)
}

// All returns every registered connector definition in registration
// order.
func All() []connector.Module {
	return extra
}
