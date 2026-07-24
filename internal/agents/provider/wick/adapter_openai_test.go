package wick

import (
	"encoding/json"
	"testing"

	"google.golang.org/genai"
)

// TestContentToOAI_TextRoles maps user/model text turns to the right
// OpenAI roles.
func TestContentToOAI_TextRoles(t *testing.T) {
	user := contentToOAI(genai.NewContentFromText("hi", genai.RoleUser))
	if len(user) != 1 || user[0].Role != "user" || user[0].Content != "hi" {
		t.Fatalf("user mapping wrong: %+v", user)
	}
	asst := contentToOAI(genai.NewContentFromText("yo", genai.RoleModel))
	if len(asst) != 1 || asst[0].Role != "assistant" || asst[0].Content != "yo" {
		t.Fatalf("assistant mapping wrong: %+v", asst)
	}
}

// TestContentToOAI_ToolCallAndResult verifies an assistant tool_call and
// the following tool result translate to the OpenAI wire shapes, keeping
// the tool_call id intact so they correlate.
func TestContentToOAI_ToolCallAndResult(t *testing.T) {
	call := &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
		{FunctionCall: &genai.FunctionCall{ID: "c1", Name: "shell", Args: map[string]any{"command": "ls"}}},
	}}
	msgs := contentToOAI(call)
	if len(msgs) != 1 || len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("want 1 assistant msg with 1 tool_call, got %+v", msgs)
	}
	tc := msgs[0].ToolCalls[0]
	if tc.ID != "c1" || tc.Function.Name != "shell" {
		t.Fatalf("tool_call fields wrong: %+v", tc)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil || args["command"] != "ls" {
		t.Fatalf("tool_call args wrong: %q", tc.Function.Arguments)
	}

	result := &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{
		{FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "shell", Response: map[string]any{"result": "file.txt"}}},
	}}
	rmsgs := contentToOAI(result)
	if len(rmsgs) != 1 || rmsgs[0].Role != "tool" || rmsgs[0].ToolCallID != "c1" || rmsgs[0].Content != "file.txt" {
		t.Fatalf("tool result mapping wrong: %+v", rmsgs)
	}
}

// TestOpenAIBuildRequest_SystemAndTools puts the system instruction first
// and translates tool declarations into lowercase-typed JSON schema.
func TestOpenAIBuildRequest_SystemAndTools(t *testing.T) {
	m := &openAIModel{modelID: "gpt-x"}
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText("be brief", genai.RoleUser),
		Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:        "shell",
			Description: "run a command",
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: map[string]*genai.Schema{"command": {Type: genai.TypeString}},
				Required:   []string{"command"},
			},
		}}}},
	}
	req := &LLMRequest{Model: "gpt-x", Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)}, Config: cfg}
	out := m.buildRequest(req)

	if len(out.Messages) != 2 || out.Messages[0].Role != "system" {
		t.Fatalf("want system message first, got %+v", out.Messages)
	}
	if len(out.Tools) != 1 || out.Tools[0].Function.Name != "shell" {
		t.Fatalf("tools not translated: %+v", out.Tools)
	}
	// schemaToJSON must lowercase the type.
	if got := out.Tools[0].Function.Parameters["type"]; got != "object" {
		t.Fatalf("schema type = %v, want lowercase 'object'", got)
	}
}

// TestOpenAIResponseParsing turns an OpenAI response into genai parts,
// including tool_calls.
func TestOpenAIResponseParsing(t *testing.T) {
	m := &openAIModel{modelID: "gpt-x"}
	var r oaiResponse
	raw := `{"choices":[{"message":{"content":"hello","tool_calls":[{"id":"c1","type":"function","function":{"name":"shell","arguments":"{\"command\":\"ls\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	resp := m.toLLMResponse(&r)
	if resp.Content == nil || len(resp.Content.Parts) != 2 {
		t.Fatalf("want 2 parts (text+call), got %+v", resp.Content)
	}
	if resp.Content.Parts[0].Text != "hello" {
		t.Errorf("text part wrong: %+v", resp.Content.Parts[0])
	}
	fc := resp.Content.Parts[1].FunctionCall
	if fc == nil || fc.Name != "shell" || fc.Args["command"] != "ls" {
		t.Errorf("function call part wrong: %+v", resp.Content.Parts[1])
	}
	if resp.UsageMetadata == nil || resp.UsageMetadata.PromptTokenCount != 10 {
		t.Errorf("usage not mapped: %+v", resp.UsageMetadata)
	}
}
