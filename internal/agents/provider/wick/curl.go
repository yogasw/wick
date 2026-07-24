package wick

import (
	"encoding/json"
	"fmt"
	"strings"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// curl.go reconstructs a "copy as curl" command for one interaction
// record, so an operator debugging "did the vendor API actually respond"
// can paste-and-run the exact request shape wick sent. Nothing about the
// vendor config (base_url, kind, api_format, key) is stored in the
// interaction log itself — it's looked up on demand from the model's
// current config here, keyed by WickModel.ID. The API key is NEVER
// resolved to plaintext for this purpose; it's always a placeholder the
// operator fills in.

// BuildCurl reconstructs a curl command for one logged interaction,
// given the model config it ran under (looked up by the caller via
// WickModel.ID = record's ModelID). recordJSON is one raw line from
// wick-interactions.jsonl. Returns an error string (not a Go error) when
// the record can't be parsed or the model config is missing/unsupported,
// so the caller can render it directly.
func BuildCurl(recordJSON []byte, m provider.WickModel) (string, error) {
	var rec interactionRecord
	if err := json.Unmarshal(recordJSON, &rec); err != nil {
		return "", fmt.Errorf("parse interaction record: %w", err)
	}

	kind := strings.ToLower(strings.TrimSpace(m.Kind))
	format := strings.ToLower(strings.TrimSpace(m.APIFormat))
	if format == "" {
		format = defaultFormatForKind(kind)
	}
	baseURL := strings.TrimRight(m.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL(kind, format)
	}

	switch format {
	case "openai_chat", "openai_responses":
		return curlOpenAI(rec, m, baseURL), nil
	case "anthropic_messages":
		return curlAnthropic(rec, m, baseURL), nil
	case "gemini":
		return curlGemini(rec, m, baseURL), nil
	default:
		return "", fmt.Errorf("unsupported api format %q for curl reconstruction", format)
	}
}

// curlOpenAI reconstructs an OpenAI Chat Completions style curl (also
// used by OpenRouter and "other" openai-compatible endpoints).
func curlOpenAI(rec interactionRecord, m provider.WickModel, baseURL string) string {
	messages := make([]map[string]string, 0, len(rec.Request)+1)
	if rec.System != "" {
		messages = append(messages, map[string]string{"role": "system", "content": rec.System})
	}
	for _, msg := range rec.Request {
		messages = append(messages, map[string]string{"role": oaiRole(msg.Role), "content": msgText(msg)})
	}
	body := map[string]any{"model": m.Model, "messages": messages}
	return renderCurl(baseURL+"/chat/completions", map[string]string{
		"Authorization": "Bearer $WICK_MODEL_API_KEY",
		"Content-Type":  "application/json",
	}, body)
}

// curlAnthropic reconstructs an Anthropic Messages API style curl.
func curlAnthropic(rec interactionRecord, m provider.WickModel, baseURL string) string {
	messages := make([]map[string]string, 0, len(rec.Request))
	for _, msg := range rec.Request {
		role := "user"
		if msg.Role == "model" || msg.Role == "assistant" {
			role = "assistant"
		}
		messages = append(messages, map[string]string{"role": role, "content": msgText(msg)})
	}
	body := map[string]any{"model": m.Model, "max_tokens": 4096, "messages": messages}
	if rec.System != "" {
		body["system"] = rec.System
	}
	return renderCurl(baseURL+"/messages", map[string]string{
		"x-api-key":         "$WICK_MODEL_API_KEY",
		"anthropic-version": anthropicVersion,
		"Content-Type":      "application/json",
	}, body)
}

// curlGemini reconstructs a Gemini generateContent style curl. The key
// is normally a query param for Gemini; kept as a placeholder the same
// way so it's never a real secret in the copyable output.
func curlGemini(rec interactionRecord, m provider.WickModel, baseURL string) string {
	contents := make([]map[string]any, 0, len(rec.Request))
	for _, msg := range rec.Request {
		role := "user"
		if msg.Role == "model" || msg.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]string{{"text": msgText(msg)}},
		})
	}
	body := map[string]any{"contents": contents}
	if rec.System != "" {
		body["systemInstruction"] = map[string]any{"parts": []map[string]string{{"text": rec.System}}}
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=$WICK_MODEL_API_KEY", baseURL, m.Model)
	return renderCurl(url, map[string]string{"Content-Type": "application/json"}, body)
}

// msgText renders one interactionMsg back into plain text for the curl
// body — tool_call/tool_result markers become a readable inline note
// since the exact vendor tool-result wire shape isn't preserved in the log.
func msgText(msg interactionMsg) string {
	switch {
	case msg.ToolCall != "":
		return "[tool_call: " + msg.ToolCall + "]"
	case msg.ToolResp != "":
		return "[tool_result] " + msg.ToolResp
	default:
		return msg.Text
	}
}

func oaiRole(role string) string {
	if role == "model" {
		return "assistant"
	}
	if role == "" {
		return "user"
	}
	return role
}

// renderCurl formats a POST curl command with headers + a pretty-printed
// JSON body, single-quoted for shell-safety.
func renderCurl(url string, headers map[string]string, body any) string {
	var b strings.Builder
	b.WriteString("curl -sS -X POST '")
	b.WriteString(url)
	b.WriteString("'")
	for k, v := range headers {
		b.WriteString(" \\\n  -H '")
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("'")
	}
	payload, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		payload = []byte("{}")
	}
	b.WriteString(" \\\n  -d '")
	b.Write(payload)
	b.WriteString("'")
	return b.String()
}
