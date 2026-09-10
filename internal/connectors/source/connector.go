// Package source exposes the session's source-control selection as a
// fixed single-instance MCP connector.
//
// A session's working directory routinely holds many cloned repos, and
// the Source panel has ONE of them selected. That selection used to live
// only in the browser, so "this repo" was a guess the agent made from
// whatever path was mentioned last — and it guessed wrong the moment the
// human switched repos. The selection now lives on the session, the
// system prompt names it, and these ops are how the agent re-reads or
// moves it.
//
// A connector rather than a hard-coded meta-tool, for the same reason
// notes and tickets are: a connector is taggable and auditable per user,
// and each op carries its own name and schema instead of being an
// action string on one overloaded tool.
//
// File layout:
//
//   - connector.go — Meta, Input structs, Operations, handler struct
//   - ops.go       — handler implementations
//
// Wire-up: connectors.Register(source.Module(layout)) before
// connectors.Service.Bootstrap.
package source

import (
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const Key = "source"

// Configs is intentionally empty — the repos are the session's own
// working directory, and git runs with the credentials already on the
// machine.
type Configs struct{}

// Meta returns the static metadata block.
func Meta() connector.Meta {
	return connector.Meta{
		Key:  Key,
		Name: "Source",
		Description: "Which repository a session is working in — the same selection the Source panel shows the human. " +
			"List the repos under the session's working directory, read the active one, or switch it.",
		Icon:  "🌿",
		Fixed: true,
	}
}

// Module returns the fully-wired connector.Module.
func Module(layout agentconfig.Layout) connector.Module {
	m := Meta()
	m.DefaultTags = []tool.DefaultTag{tags.Connector, tags.Platform}
	return connector.Module{
		Meta:       m,
		Operations: Operations(layout),
	}
}

// Operations builds the op list, capturing layout so handlers can reach
// the session store.
func Operations(layout agentconfig.Layout) []connector.Category {
	h := &handlers{layout: layout}
	return []connector.Category{connector.Cat("Repositories",
		"The git repositories under the calling session's working directory, and which one is active. "+
			"Scope defaults to the calling session, so these ops normally need no arguments.",
		connector.Op("source_active", "Active Repository",
			"Report the repository this session is working in: its rel handle, absolute dir, branch, and "+
				"changed / ahead / behind counts. "+
				"explicit=false means nobody has picked one and this is simply the first repo found. "+
				"Your system prompt already names it as active_repo — call this when that looks stale, or after "+
				"someone may have switched the panel.",
			scopeInput{}, h.active, wickdocs.Docs{}),
		connector.Op("source_list", "List Repositories",
			"List every git repository under the session's working directory, with branch and change counts, and "+
				"which one is active. Use it to answer \"which repos are here\" and to get the rel handle "+
				"source_select expects.",
			scopeInput{}, h.list, wickdocs.Docs{}),
		connector.Op("source_changes", "Changed Files",
			"List the files changed in a repository right now: path, staged / unstaged / untracked, and the "+
				"git status codes. Defaults to the active repo. "+
				"This is deliberately NOT in your system prompt \u2014 the list changes every few seconds, and a "+
				"snapshot from spawn time would describe files nobody is editing any more. Call it when you need "+
				"to know what is in flight: before proposing a commit, when the user says \"what did I change\", "+
				"or to check whether your own edit landed.",
			changesInput{}, h.changes, wickdocs.Docs{}),
		connector.Op("source_select", "Select Repository",
			"Switch the repository this session works in. This MOVES THE HUMAN'S Source panel too, so only call it "+
				"when the user asks to work somewhere else — not to look something up, which needs no selection. "+
				"repo accepts the rel handle from source_list, the repo's name, or its absolute path. "+
				"Pass an empty repo to clear the pick and fall back to the first repo found.",
			selectInput{}, h.selectRepo, wickdocs.Docs{}),
	)}
}

/* ── Inputs ──────────────────────────────────────────────────────────── */

// scopeInput selects which session's repos to look at. Empty = the
// calling session, which is the common case.
type scopeInput struct {
	SessionID string `wick:"desc=Session whose working directory to inspect. Defaults to the calling session."`
}

type changesInput struct {
	Repo      string `wick:"desc=Repository to inspect: the rel handle from source_list, its name, or its absolute path. Defaults to the active repo."`
	SessionID string `wick:"desc=Session whose working directory to inspect. Defaults to the calling session."`
}

type selectInput struct {
	Repo      string `wick:"desc=Repository to activate: the rel handle from source_list, its name, or its absolute path. Empty clears the selection."`
	SessionID string `wick:"desc=Session to switch. Defaults to the calling session."`
}
