package nodes

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

// newThinkingTestPool builds a real *pool.Pool over a temp layout. No
// factory is wired — the tests only exercise EnsureSession +
// SetThinkingTokens, neither of which spawns.
func newThinkingTestPool(t *testing.T) (*pool.Pool, config.Layout) {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return pool.New(pool.PoolConfig{Layout: layout}), layout
}

func TestAgentNodePersistsThinkingTokens(t *testing.T) {
	cases := []struct {
		name      string
		thinking  string
		maxTokens int
		want      string
	}{
		{name: "off disables thinking", thinking: "off", want: "0"},
		{name: "off ignores token field", thinking: "off", maxTokens: 4096, want: "0"},
		{name: "on unlimited when zero", thinking: "on", maxTokens: 0, want: ""},
		{name: "on caps at budget", thinking: "on", maxTokens: 2048, want: "2048"},
		{name: "legacy default keeps default", thinking: "default", maxTokens: 0, want: ""},
		{name: "missing keeps default", thinking: "", maxTokens: 0, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, layout := newThinkingTestPool(t)
			e := NewAgentExecutor(nil, p, nil)
			id := "wf_adhoc_thinking_test"
			if err := p.EnsureSession(context.Background(), id, "workflow", ""); err != nil {
				t.Fatalf("ensure session: %v", err)
			}
			n := workflow.Node{ID: "ask", Type: workflow.NodeAgent, Thinking: tc.thinking, MaxThinkingTokens: tc.maxTokens}
			if err := e.persistAgentSessionConfig(id, n); err != nil {
				t.Fatalf("persistAgentSessionConfig: %v", err)
			}
			s, err := session.Load(layout, id)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			var got string
			for _, a := range s.Agents {
				if a.Name == "default" {
					got = a.ThinkingTokens
				}
			}
			if got != tc.want {
				t.Fatalf("thinking=%q tokens=%d → ThinkingTokens=%q, want %q", tc.thinking, tc.maxTokens, got, tc.want)
			}
		})
	}
}

// TestAgentSchemaHasThinkingControls asserts the node schema exposes the
// thinking dropdown (on|off) and the conditional max_thinking_tokens field
// so the editor renders the control and reveals the budget field on "on".
func TestAgentSchemaHasThinkingControls(t *testing.T) {
	schema := integration.StructSchema(agentSchema{})
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties: %+v", schema)
	}
	thinking, ok := props["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing thinking property: %+v", props)
	}
	enum, ok := thinking["enum"].([]string)
	if !ok || strings.Join(enum, ",") != "on,off" {
		t.Fatalf("thinking enum want [on off], got %v (%T)", thinking["enum"], thinking["enum"])
	}
	mtt, ok := props["max_thinking_tokens"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing max_thinking_tokens property: %+v", props)
	}
	if mtt["visible_when"] != "thinking:on" {
		t.Fatalf("max_thinking_tokens visible_when = %v, want thinking:on", mtt["visible_when"])
	}
}
