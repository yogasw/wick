package wick

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// curl.go reconstructs the request one logged interaction sent, so an
// operator debugging "did the vendor API actually respond" can replay the
// exact shape. Nothing about the vendor config (base_url, kind, api_format,
// key) is stored in the interaction log — it's looked up on demand from the
// model's current config, keyed by WickModel.ID.
//
// BuildRequest produces a structured CurlRequest (method/url/headers/body);
// the renderers turn it into whatever the caller needs: a Postman-safe
// single line, a readable multi-line bash command, a raw HTTP request
// (Postman/Insomnia "import raw"), or just the JSON body. The API key is
// injected at render time as a bearerValue — the struct never carries a
// secret, so the same request can be rendered with a placeholder OR (admin,
// on request) the real key without re-deriving anything.

// CurlRequest is the structured, render-agnostic form of a reconstructed
// request. AuthHeader names the header the bearer/token goes in (differs by
// vendor: Authorization vs x-api-key vs a query param for Gemini).
type CurlRequest struct {
	Method string
	URL    string // may contain a {{KEY}} marker for Gemini's query-param auth
	// Headers excludes the auth header; that's applied at render with the
	// caller's chosen bearer value (placeholder / custom / real).
	Headers    map[string]string
	AuthHeader string // "" when auth is a URL query param (Gemini)
	AuthScheme string // "Bearer " for OpenAI; "" for x-api-key / query param
	Body       json.RawMessage
	// EnvHint lists the env vars a caller may want to export before running
	// (shown in the UI's env block). Values are placeholders, never secrets.
	EnvHint map[string]string
}

// bearerPlaceholder is the shell var the key defaults to, so a copied curl
// never contains a real secret unless the operator explicitly reveals it.
const bearerPlaceholder = "$WICK_MODEL_API_KEY"

// BuildRequest reconstructs the structured request for one logged
// interaction under the given model config. Returns an error string (not a
// Go error) so the caller can render it directly.
func BuildRequest(recordJSON []byte, m provider.WickModel) (CurlRequest, error) {
	var rec interactionRecord
	if err := json.Unmarshal(recordJSON, &rec); err != nil {
		return CurlRequest{}, fmt.Errorf("parse interaction record: %w", err)
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
		return reqOpenAI(rec, m, baseURL), nil
	case "anthropic_messages":
		return reqAnthropic(rec, m, baseURL), nil
	case "gemini":
		return reqGemini(rec, m, baseURL), nil
	default:
		return CurlRequest{}, fmt.Errorf("unsupported api format %q for curl reconstruction", format)
	}
}

// BuildCurl keeps the original single-shot API: a ready single-line curl
// with the key as a placeholder. Kept for existing callers/tests; new UI
// uses BuildRequest + a renderer.
func BuildCurl(recordJSON []byte, m provider.WickModel) (string, error) {
	req, err := BuildRequest(recordJSON, m)
	if err != nil {
		return "", err
	}
	return RenderMultiline(req, bearerPlaceholder), nil
}

func reqOpenAI(rec interactionRecord, m provider.WickModel, baseURL string) CurlRequest {
	messages := make([]map[string]string, 0, len(rec.Request)+1)
	if rec.System != "" {
		messages = append(messages, map[string]string{"role": "system", "content": rec.System})
	}
	for _, msg := range rec.Request {
		messages = append(messages, map[string]string{"role": oaiRole(msg.Role), "content": msgText(msg)})
	}
	body, _ := json.MarshalIndent(map[string]any{"model": m.Model, "messages": messages}, "", "  ")
	return CurlRequest{
		Method:     "POST",
		URL:        baseURL + "/chat/completions",
		Headers:    map[string]string{"Content-Type": "application/json"},
		AuthHeader: "Authorization",
		AuthScheme: "Bearer ",
		Body:       body,
		EnvHint:    map[string]string{"WICK_MODEL_API_KEY": "your OpenAI-compatible API key", "BASE_URL": baseURL},
	}
}

func reqAnthropic(rec interactionRecord, m provider.WickModel, baseURL string) CurlRequest {
	messages := make([]map[string]string, 0, len(rec.Request))
	for _, msg := range rec.Request {
		role := "user"
		if msg.Role == "model" || msg.Role == "assistant" {
			role = "assistant"
		}
		messages = append(messages, map[string]string{"role": role, "content": msgText(msg)})
	}
	payload := map[string]any{"model": m.Model, "max_tokens": 4096, "messages": messages}
	if rec.System != "" {
		payload["system"] = rec.System
	}
	body, _ := json.MarshalIndent(payload, "", "  ")
	return CurlRequest{
		Method:     "POST",
		URL:        baseURL + "/messages",
		Headers:    map[string]string{"anthropic-version": anthropicVersion, "Content-Type": "application/json"},
		AuthHeader: "x-api-key",
		AuthScheme: "",
		Body:       body,
		EnvHint:    map[string]string{"WICK_MODEL_API_KEY": "your Anthropic API key", "BASE_URL": baseURL},
	}
}

func reqGemini(rec interactionRecord, m provider.WickModel, baseURL string) CurlRequest {
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
	payload := map[string]any{"contents": contents}
	if rec.System != "" {
		payload["systemInstruction"] = map[string]any{"parts": []map[string]string{{"text": rec.System}}}
	}
	body, _ := json.MarshalIndent(payload, "", "  ")
	// Gemini takes the key as a ?key= query param, not a header. The URL
	// carries a {{KEY}} marker the renderer substitutes with the bearer.
	return CurlRequest{
		Method:     "POST",
		URL:        fmt.Sprintf("%s/v1beta/models/%s:generateContent?key={{KEY}}", baseURL, m.Model),
		Headers:    map[string]string{"Content-Type": "application/json"},
		AuthHeader: "", // query-param auth
		AuthScheme: "",
		Body:       body,
		EnvHint:    map[string]string{"WICK_MODEL_API_KEY": "your Gemini API key", "BASE_URL": baseURL},
	}
}

// ── renderers ────────────────────────────────────────────────────────

// applyAuth returns the URL and the auth header (name,value) for a bearer.
// For Gemini (query-param auth) the bearer is spliced into the URL's {{KEY}}
// marker and no header is returned.
func applyAuth(r CurlRequest, bearer string) (url string, authName, authValue string) {
	url = r.URL
	if strings.Contains(url, "{{KEY}}") {
		url = strings.ReplaceAll(url, "{{KEY}}", bearer)
		return url, "", ""
	}
	if r.AuthHeader != "" {
		return url, r.AuthHeader, r.AuthScheme + bearer
	}
	return url, "", ""
}

// sortedHeaderKeys gives headers a stable render order (auth first, then
// alphabetical) so the output is deterministic across calls/tests.
func sortedHeaderKeys(h map[string]string) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// RenderSingleLine renders a one-line curl (no `\` continuation), which
// pastes into Postman "Import → Raw text" without the truncation the
// multi-line form suffers.
func RenderSingleLine(r CurlRequest, bearer string) string {
	url, authName, authValue := applyAuth(r, bearer)
	var b strings.Builder
	fmt.Fprintf(&b, "curl -sS -X %s %s", r.Method, sq(url))
	if authName != "" {
		fmt.Fprintf(&b, " -H %s", sq(authName+": "+authValue))
	}
	for _, k := range sortedHeaderKeys(r.Headers) {
		fmt.Fprintf(&b, " -H %s", sq(k+": "+r.Headers[k]))
	}
	if len(r.Body) > 0 {
		fmt.Fprintf(&b, " -d %s", sq(compactJSON(r.Body)))
	}
	return b.String()
}

// RenderMultiline renders the readable bash form with `\` continuation.
func RenderMultiline(r CurlRequest, bearer string) string {
	url, authName, authValue := applyAuth(r, bearer)
	var b strings.Builder
	fmt.Fprintf(&b, "curl -sS -X %s %s", r.Method, sq(url))
	if authName != "" {
		fmt.Fprintf(&b, " \\\n  -H %s", sq(authName+": "+authValue))
	}
	for _, k := range sortedHeaderKeys(r.Headers) {
		fmt.Fprintf(&b, " \\\n  -H %s", sq(k+": "+r.Headers[k]))
	}
	if len(r.Body) > 0 {
		fmt.Fprintf(&b, " \\\n  -d %s", sq(string(prettyJSON(r.Body))))
	}
	return b.String()
}

// sq wraps s in single quotes for a POSIX shell, escaping any embedded
// single quote via the '\'' idiom (close quote, escaped quote, reopen).
// Without this a body containing an apostrophe (e.g. "don't" in a system
// prompt) terminated the -d '...' string early — which is exactly what
// truncated the request when pasted into Postman's curl importer.
func sq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// RenderRawHTTP renders a raw HTTP/1.1 request — the format Postman /
// Insomnia "import raw" parse most reliably.
func RenderRawHTTP(r CurlRequest, bearer string) string {
	url, authName, authValue := applyAuth(r, bearer)
	host, path := splitHostPath(url)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\n", r.Method, path)
	fmt.Fprintf(&b, "Host: %s\n", host)
	if authName != "" {
		fmt.Fprintf(&b, "%s: %s\n", authName, authValue)
	}
	for _, k := range sortedHeaderKeys(r.Headers) {
		fmt.Fprintf(&b, "%s: %s\n", k, r.Headers[k])
	}
	if len(r.Body) > 0 {
		b.WriteString("\n")
		b.Write(prettyJSON(r.Body))
	}
	return b.String()
}

// RenderJSONBody returns just the pretty-printed request body.
func RenderJSONBody(r CurlRequest) string {
	if len(r.Body) == 0 {
		return "{}"
	}
	return string(prettyJSON(r.Body))
}

// splitHostPath breaks a URL into host and path+query for the raw-HTTP
// request line. Best-effort: an unparsable URL degrades to ("", url).
func splitHostPath(url string) (host, path string) {
	s := url
	for _, pre := range []string{"https://", "http://"} {
		if strings.HasPrefix(s, pre) {
			s = s[len(pre):]
			break
		}
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i], s[i:]
	}
	return s, "/"
}

func compactJSON(b json.RawMessage) string {
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		return string(b)
	}
	return out.String()
}

func prettyJSON(b json.RawMessage) []byte {
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return b
	}
	return out.Bytes()
}

// msgText renders one interactionMsg back into plain text for the request
// body — tool_call/tool_result markers become a readable inline note since
// the exact vendor tool-result wire shape isn't preserved in the log.
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
