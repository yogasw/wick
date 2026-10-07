package view

import "testing"

// The sidebar's New session must stay in Agents: the bare landing is what
// "Open Team when I open Agents" redirects to Team, so the unscoped link
// carries ?view=classic, and a scoped one keeps its ?project=.
func TestNewSessionHrefStaysInAgents(t *testing.T) {
	if got := (AgentsLayoutVM{Base: "/tools/agents"}).NewSessionHref(); got != "/tools/agents/?view=classic" {
		t.Fatalf("unscoped = %q, want /tools/agents/?view=classic", got)
	}
	if got := (AgentsLayoutVM{Base: "/tools/agents", ScopedProjectID: "p1"}).NewSessionHref(); got != "/tools/agents/?project=p1" {
		t.Fatalf("scoped = %q, want /tools/agents/?project=p1", got)
	}
}
