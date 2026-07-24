package wick

import (
	"encoding/json"
	"testing"

	"google.golang.org/genai"
)

// TestAnthropicBuildRequest maps system, messages, and tools, and always
// sets a max_tokens (the Messages API requires it).
func TestAnthropicBuildRequest(t *testing.T) {
	m := &anthropicModel{modelID: "claude-x"}
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText("be brief", genai.RoleUser),
		Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:       "shell",
			Parameters: &genai.Schema{Type: genai.TypeObject},
		}}}},
	}
	req := &LLMRequest{
		Model:    "claude-x",
		Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)},
		Config:   cfg,
	}
	out := m.buildRequest(req)
	if len(out.System) != 1 || out.System[0].Text != "be brief" {
		t.Fatalf("system wrong: %+v", out.System)
	}
	// Prompt caching: system + last tool must carry an ephemeral cache breakpoint.
	if out.System[0].CacheControl == nil || out.System[0].CacheControl.Type != "ephemeral" {
		t.Errorf("system missing cache_control: %+v", out.System[0])
	}
	if n := len(out.Tools); n > 0 && (out.Tools[n-1].CacheControl == nil) {
		t.Errorf("last tool missing cache_control: %+v", out.Tools)
	}
	if out.MaxTokens != anthropicDefaultMaxTokens {
		t.Errorf("max_tokens default not set: %d", out.MaxTokens)
	}
	if len(out.Tools) != 1 || out.Tools[0].Name != "shell" {
		t.Errorf("tools not translated: %+v", out.Tools)
	}
	if len(out.Messages) != 1 || out.Messages[0].Role != "user" {
		t.Errorf("messages wrong: %+v", out.Messages)
	}
}

// TestAnthropicToolResultBlock: a function response becomes a user
// message with a tool_result block correlating on tool_use_id.
func TestAnthropicToolResultBlock(t *testing.T) {
	c := &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{
		{FunctionResponse: &genai.FunctionResponse{ID: "t1", Name: "shell", Response: map[string]any{"result": "ok"}}},
	}}
	msg, ok := contentToAnthropic(c)
	if !ok || len(msg.Content) != 1 {
		t.Fatalf("want 1 block, got ok=%v %+v", ok, msg)
	}
	b := msg.Content[0]
	if b.Type != "tool_result" || b.ToolUseID != "t1" || b.Content != "ok" {
		t.Fatalf("tool_result block wrong: %+v", b)
	}
}

// TestAnthropicResponseParsing turns a Messages response (text +
// tool_use) into genai parts.
func TestAnthropicResponseParsing(t *testing.T) {
	m := &anthropicModel{modelID: "claude-x"}
	var r antResponse
	raw := `{"content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"t1","name":"shell","input":{"command":"ls"}}],"usage":{"input_tokens":7,"output_tokens":3}}`
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	resp := m.toLLMResponse(&r)
	if resp.Content == nil || len(resp.Content.Parts) != 2 {
		t.Fatalf("want 2 parts, got %+v", resp.Content)
	}
	if resp.Content.Parts[0].Text != "hi" {
		t.Errorf("text part wrong: %+v", resp.Content.Parts[0])
	}
	fc := resp.Content.Parts[1].FunctionCall
	if fc == nil || fc.Name != "shell" || fc.Args["command"] != "ls" {
		t.Errorf("tool_use part wrong: %+v", resp.Content.Parts[1])
	}
	if resp.UsageMetadata == nil || resp.UsageMetadata.PromptTokenCount != 7 {
		t.Errorf("usage not mapped: %+v", resp.UsageMetadata)
	}
}
