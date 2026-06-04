// Package rest — OpenAI Responses API compatible handler.
//
// POST /integrations/rest/api/v1/openai/responses mirrors OpenAI's newer Responses
// API surface (https://platform.openai.com/docs/api-reference/responses).
// Conversation continuity is keyed by previous_response_id: the id wick
// returns on response N can be sent back on request N+1 to reuse the
// same underlying wick session (history stays server-side). Omit
// previous_response_id for a fresh stateless turn — the input is sent
// as-is. Streaming, tool calls, function calls, and structured-output
// formats are not supported; the assistant text is returned as a single
// output_text content part.

package rest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// responsesRequest is the subset of OpenAI's Responses API request that
// wick honours. Unknown fields decode silently (json.Unmarshal default)
// so clients sending extras (tools, reasoning, text formatting, …) don't
// 400 — the extras are just ignored. Input accepts either a plain string
// or the array form ([]inputItem); see decodeInput.
type responsesRequest struct {
	Model              string            `json:"model"`
	Input              json.RawMessage   `json:"input"`
	Instructions       string            `json:"instructions"`
	PreviousResponseID string            `json:"previous_response_id"`
	Conversation       string            `json:"conversation"`
	User               string            `json:"user"`
	Stream             bool              `json:"stream"`
	Metadata           map[string]string `json:"metadata"`
	// Project optionally names the wick Project (id) for this request,
	// overriding the channel default. Also via metadata.project[_id].
	Project string `json:"project"`
}

// inputItem is one entry of the array form of `input`. content is either
// a string or an array of content parts; only text parts are extracted.
type inputItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// contentPart is one entry of an inputItem.content array.
type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// responsesResponse mirrors OpenAI's response.object shape closely
// enough for SDKs (openai-python ≥1.40, openai-node ≥4.55) to deserialise.
// Token counts are zeroed — wick does not run a tokenizer.
type responsesResponse struct {
	ID                 string             `json:"id"`
	Object             string             `json:"object"`
	CreatedAt          int64              `json:"created_at"`
	Status             string             `json:"status"`
	Model              string             `json:"model"`
	PreviousResponseID *string            `json:"previous_response_id"`
	Output             []responsesOutput  `json:"output"`
	OutputText         string             `json:"output_text"`
	Usage              responsesUsage     `json:"usage"`
	Metadata           map[string]string  `json:"metadata"`
}

type responsesOutput struct {
	Type    string                 `json:"type"`
	ID      string                 `json:"id"`
	Status  string                 `json:"status"`
	Role    string                 `json:"role"`
	Content []responsesContentPart `json:"content"`
}

type responsesContentPart struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}

type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// responsesIDPrefix tags ids wick mints so previous_response_id can be
// decoded back into the underlying wick session id without a lookup table.
const responsesIDPrefix = "resp_"

func (c *Channel) handleResponses(w http.ResponseWriter, r *http.Request) {
	if status, msg := c.checkReady(); status != 0 {
		writeError(w, status, msg)
		return
	}
	userID, status, msg := c.authBearer(r)
	if status != 0 {
		writeError(w, status, msg)
		return
	}

	var req responsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.Stream {
		writeError(w, http.StatusBadRequest, "streaming not supported on this endpoint")
		return
	}
	if !IsModelAllowed(req.Model) {
		writeModelNotFound(w, req.Model)
		return
	}

	userInput, err := decodeInput(req.Input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Resolve session in priority order:
	//   1. previous_response_id — OpenAI Responses chaining
	//   2. conversation         — explicit thread key (field or metadata)
	//   3. fresh UUID           — stateless turn
	// Stateful modes send only the new turn (instructions + input);
	// stateless prepends instructions inline.
	var (
		sessionID string
		reused    bool
	)
	switch {
	case strings.TrimSpace(req.PreviousResponseID) != "":
		base := strings.TrimPrefix(strings.TrimSpace(req.PreviousResponseID), responsesIDPrefix)
		sessionID = "rest-" + base
		reused = true
	default:
		if key := resolveConversation(req.Conversation, req.Metadata); key != "" {
			sessionID = "rest-" + key
			reused = true
		} else {
			sessionID = "rest-" + uuid.NewString()
		}
	}

	prompt := composeResponsesPrompt(req.Instructions, userInput, reused)
	if strings.TrimSpace(prompt) == "" {
		writeError(w, http.StatusBadRequest, "input is required")
		return
	}

	res, status, msg := c.dispatch(r.Context(), sessionID, userID, req.User, prompt, reused, resolveProject(req.Project, req.Metadata))
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	if res.errMsg != "" {
		s := http.StatusInternalServerError
		if res.blocked {
			s = http.StatusForbidden
		}
		writeError(w, s, res.errMsg)
		return
	}

	id := responsesIDPrefix + strings.TrimPrefix(sessionID, "rest-")
	now := time.Now()
	resp := responsesResponse{
		ID:        id,
		Object:    "response",
		CreatedAt: now.Unix(),
		Status:    "completed",
		Model:     firstNonEmpty(req.Model, "wick"),
		Output: []responsesOutput{{
			Type:   "message",
			ID:     "msg_" + uuid.NewString(),
			Status: "completed",
			Role:   "assistant",
			Content: []responsesContentPart{{
				Type:        "output_text",
				Text:        res.text,
				Annotations: []any{},
			}},
		}},
		OutputText: res.text,
		Usage:      responsesUsage{},
		Metadata:   req.Metadata,
	}
	if reused {
		v := req.PreviousResponseID
		resp.PreviousResponseID = &v
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// decodeInput accepts either a JSON string or an array of input items
// and returns the user-facing text wick should treat as the new turn.
// Empty payload returns ("", nil) — handler maps that to a 400.
func decodeInput(raw json.RawMessage) (string, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}

	// String form: input: "hello".
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("invalid input string: %w", err)
		}
		return s, nil
	}

	// Array form: input: [{type, role, content}, ...].
	if raw[0] == '[' {
		var items []inputItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return "", fmt.Errorf("invalid input array: %w", err)
		}
		return flattenInputItems(items), nil
	}

	return "", fmt.Errorf("input must be a string or array")
}

// flattenInputItems renders the array form of `input` into a single
// prompt. Mirrors flattenMessages logic: system / prior assistant turns
// are tagged, the trailing user turn stays raw so it reads as the live
// prompt.
func flattenInputItems(items []inputItem) string {
	// Pre-extract text per item and find the index of the last user turn.
	texts := make([]string, len(items))
	lastUser := -1
	for i, it := range items {
		texts[i] = extractItemText(it.Content)
		if it.Role == "user" && strings.TrimSpace(texts[i]) != "" {
			lastUser = i
		}
	}
	if lastUser == -1 {
		return ""
	}
	var b strings.Builder
	for i, it := range items {
		text := strings.TrimSpace(texts[i])
		if text == "" {
			continue
		}
		role := it.Role
		if role == "" {
			role = it.Type
		}
		switch role {
		case "system", "developer":
			b.WriteString("[system] ")
			b.WriteString(text)
		case "assistant":
			b.WriteString("[assistant] ")
			b.WriteString(text)
		case "user":
			if i == lastUser {
				b.WriteString(text)
			} else {
				b.WriteString("[user] ")
				b.WriteString(text)
			}
		default:
			b.WriteString("[" + role + "] ")
			b.WriteString(text)
		}
		if i < len(items)-1 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

// extractItemText pulls text out of an inputItem.content payload. Accepts
// either a string or an array of content parts ({type:"input_text"|"output_text", text}).
func extractItemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	if raw[0] == '[' {
		var parts []contentPart
		if json.Unmarshal(raw, &parts) != nil {
			return ""
		}
		var b strings.Builder
		for _, p := range parts {
			if p.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// composeResponsesPrompt builds the prompt wick sends to the agent. In
// stateless mode (no previous_response_id) instructions are prefixed
// once; in reused mode instructions are skipped because the agent
// already has them from turn one, and re-sending would duplicate the
// system block on every call.
func composeResponsesPrompt(instructions, input string, reused bool) string {
	input = strings.TrimSpace(input)
	instructions = strings.TrimSpace(instructions)
	if reused || instructions == "" {
		return input
	}
	if input == "" {
		return ""
	}
	return "[system] " + instructions + "\n\n" + input
}
