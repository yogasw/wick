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
// MCP tool names are produced by combining all three:
//
//	{connector_key}__{op_key}__{instance_label_slug}
//
// For example a Loki connector with operations "query" and "list_apps",
// duplicated as instances "Prod" and "Staging", surfaces four MCP tools:
//
//	loki__query__prod
//	loki__list_apps__prod
//	loki__query__staging
//	loki__list_apps__staging
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

import "github.com/yogasw/wick/pkg/entity"

// Meta is the static metadata for a connector definition. Key must be a
// unique slug across every connector — MCP tool names start with it,
// and entity.Connector.Key references it (one Meta.Key, many entity
// rows for multi-instance setups).
//
// Description is shown to the admin and (combined with the Operation
// description) to the LLM in MCP tools/list. Keep it short; the
// per-operation Description is the load-bearing signal for the model.
type Meta struct {
	Key         string
	Name        string
	Description string
	Icon        string
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
// verbatim in MCP tools/list and is the primary signal the model uses
// to decide whether to call this op. Use action verbs and be specific
// ("List repositories visible to the authenticated user", not "list").
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
}

// Op is a small constructor that reflects a typed input struct into
// the Operation's Input rows. Equivalent to building Operation{} by
// hand and calling entity.StructToConfigs(input) yourself, but reads
// nicer when listing many operations inline.
//
//	connector.Op("query", "Query Logs",
//	    "Search Loki using LogQL.",
//	    QueryInput{}, queryExec)
//
// Pass struct{}{} when the operation takes no input arguments.
func Op[I any](key, name, description string, input I, exec ExecuteFunc) Operation {
	return Operation{
		Key:         key,
		Name:        name,
		Description: description,
		Input:       entity.StructToConfigs(input),
		Execute:     exec,
	}
}

// OpDestructive is the destructive-marked variant of Op. The resulting
// Operation defaults to disabled when a new instance is created, and
// the admin UI flags it so admins know to verify before enabling.
func OpDestructive[I any](key, name, description string, input I, exec ExecuteFunc) Operation {
	op := Op(key, name, description, input, exec)
	op.Destructive = true
	return op
}

// Module is the internal, fully-resolved registration record wick keeps
// for every connector definition. It is produced by app.RegisterConnector
// — the Meta, the configs reflected from the typed Creds struct, and
// the list of operations the connector exposes. Downstream code does
// not construct Module directly.
type Module struct {
	Meta       Meta
	Configs    []entity.Config
	Operations []Operation
}
