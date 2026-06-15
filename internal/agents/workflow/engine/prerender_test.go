package engine

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// TestPreRenderNode_DoesNotMutateSharedNode is a regression test for the
// shared-node arg-bleed bug.
//
// The trigger router caches each workflow definition and reuses the same
// Node objects across every run (see trigger/router.go: r.defs[id]). A Node
// is passed to preRenderNode by value, but its map/slice fields (Args,
// ArgModes, Headers, Query, ShellEnv, Command, SQLArgs, ...) still alias the
// cached node's backing storage. If preRenderNode renders templates in place,
// the first run replaces "{{...}}" with a literal and every later run reads
// that frozen literal instead of re-rendering against the new event.
//
// Concretely this caused a notify connector to keep sending an old PR's
// rendered body/title/url no matter which PR triggered the workflow. The fix
// must render into a per-run copy and leave the source node untouched.
func TestPreRenderNode_DoesNotMutateSharedNode(t *testing.T) {
	const tmplTitle = "PR #{{.Event.Payload.number}}"
	const tmplNum = "{{.Event.Payload.number}}"

	// Shared, cached node — mimics what the router hands to the engine on
	// every run.
	n := workflow.Node{
		ID:      "notify",
		Type:    workflow.NodeConnector,
		Module:  "notifications",
		Op:      "send_to_push_id",
		Args:    map[string]any{"title": tmplTitle},
		Headers: map[string]string{"X-Num": tmplNum},
		Command: []string{"echo", tmplNum},
	}

	ctxOf := func(num string) workflow.RenderCtx {
		return workflow.RenderCtx{
			Event: workflow.Event{
				Type:    "webhook",
				Payload: map[string]any{"number": num},
			},
		}
	}

	// Run 1: number = 111.
	out1, err := preRenderNode(n, ctxOf("111"))
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if got := out1.Args["title"]; got != "PR #111" {
		t.Fatalf("run 1 Args[title] = %q, want %q", got, "PR #111")
	}
	if got := out1.Headers["X-Num"]; got != "111" {
		t.Fatalf("run 1 Headers[X-Num] = %q, want %q", got, "111")
	}
	if got := out1.Command[1]; got != "111" {
		t.Fatalf("run 1 Command[1] = %q, want %q", got, "111")
	}

	// The shared definition must be untouched after run 1 — the templates
	// must still be intact so the next run can render them afresh.
	if got := n.Args["title"]; got != tmplTitle {
		t.Fatalf("source Args[title] mutated by run 1: %q, want template %q", got, tmplTitle)
	}
	if got := n.Headers["X-Num"]; got != tmplNum {
		t.Fatalf("source Headers[X-Num] mutated by run 1: %q, want template %q", got, tmplNum)
	}
	if got := n.Command[1]; got != tmplNum {
		t.Fatalf("source Command[1] mutated by run 1: %q, want template %q", got, tmplNum)
	}

	// Run 2: number = 222. Must render fresh, never reuse 111 from run 1.
	out2, err := preRenderNode(n, ctxOf("222"))
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if got := out2.Args["title"]; got != "PR #222" {
		t.Fatalf("run 2 Args[title] = %q, want %q (frozen from run 1?)", got, "PR #222")
	}
	if got := out2.Headers["X-Num"]; got != "222" {
		t.Fatalf("run 2 Headers[X-Num] = %q, want %q (frozen from run 1?)", got, "222")
	}
	if got := out2.Command[1]; got != "222" {
		t.Fatalf("run 2 Command[1] = %q, want %q (frozen from run 1?)", got, "222")
	}
}
