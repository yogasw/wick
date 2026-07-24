package wick

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// TestExternalToolProvider_FlowsToEngine: a host-provided tool (the MCP
// surface) is offered to the model, called, and its result flows back —
// end-to-end through the real ClaudeParser.
func TestExternalToolProvider_FlowsToEngine(t *testing.T) {
	// Register a fake host provider exposing an "ask_user"-like tool.
	called := ""
	SetToolProvider(func(scope ToolScope) []ExternalTool {
		return []ExternalTool{{
			Name:        "ask_user",
			Description: "ask the user a question",
			Params: map[string]any{
				"type":       "object",
				"properties": map[string]any{"question": map[string]any{"type": "string"}},
				"required":   []any{"question"},
			},
			Handler: func(ctx context.Context, args map[string]any) (string, bool) {
				called, _ = args["question"].(string)
				return "user says hi", false
			},
		}}
	})
	t.Cleanup(func() { SetToolProvider(nil) })

	// buildTools must include the external tool alongside shell/todo.
	tools := buildTools(toolContext{SessionID: "s1"})
	var haveAsk bool
	for _, td := range tools {
		if td.decl.Name == "ask_user" {
			haveAsk = true
			// Schema converted from JSON-Schema map → genai.
			if td.decl.Parameters == nil || td.decl.Parameters.Type != genai.TypeObject {
				t.Errorf("ask_user schema not converted: %+v", td.decl.Parameters)
			}
		}
	}
	if !haveAsk {
		t.Fatalf("external tool ask_user not in buildTools output: %d tools", len(tools))
	}

	// Drive the engine: model calls ask_user, then answers.
	f := &fakeModel{responses: []*LLMResponse{
		toolCallResp("ask_user", map[string]any{"question": "ok?"}),
		textResp("done"),
	}}
	var lines []string
	emit := func(b []byte) { lines = append(lines, string(b)) }
	eng := newEngine(f, "fake", "", &genai.GenerateContentConfig{}, tools, nil, 0, emit)
	eng.start()
	eng.runTurn(context.Background(), "hello")

	if called != "ok?" {
		t.Errorf("external tool handler not invoked with args, got %q", called)
	}
	evs := collectParsed(t, lines)
	var gotResult bool
	for _, e := range evs {
		if strings.Contains(e.Text, "user says hi") {
			gotResult = true
		}
	}
	if !gotResult {
		t.Errorf("external tool result did not flow back: %+v", evs)
	}
}

// TestExternalToolProvider_NilSafe: no provider → only built-ins, no panic.
func TestExternalToolProvider_NilSafe(t *testing.T) {
	SetToolProvider(nil)
	tools := buildTools(toolContext{})
	// shell + todo present, nothing external.
	for _, td := range tools {
		if td.decl.Name == "ask_user" {
			t.Fatal("no external tool should be present with nil provider")
		}
	}
	if len(tools) < 2 {
		t.Errorf("expected built-in shell+todo, got %d", len(tools))
	}
}

// TestJSONSchemaToGenai covers the map→genai conversion used for external
// tool params.
func TestJSONSchemaToGenai(t *testing.T) {
	m := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "description": "the name"},
			"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"age":  map[string]any{"type": "integer"},
		},
		"required": []any{"name"},
	}
	s := jsonSchemaToGenai(m)
	if s.Type != genai.TypeObject {
		t.Fatalf("root type = %v", s.Type)
	}
	if s.Properties["name"].Type != genai.TypeString || s.Properties["name"].Description != "the name" {
		t.Errorf("name prop wrong: %+v", s.Properties["name"])
	}
	if s.Properties["tags"].Type != genai.TypeArray || s.Properties["tags"].Items == nil || s.Properties["tags"].Items.Type != genai.TypeString {
		t.Errorf("tags array/items wrong: %+v", s.Properties["tags"])
	}
	if s.Properties["age"].Type != genai.TypeInteger {
		t.Errorf("age type wrong: %+v", s.Properties["age"])
	}
	if len(s.Required) != 1 || s.Required[0] != "name" {
		t.Errorf("required wrong: %+v", s.Required)
	}
}
