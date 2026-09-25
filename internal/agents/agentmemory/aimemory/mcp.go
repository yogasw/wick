package aimemory

// The one MCP call this dashboard makes.
//
// Everything else reads the CLI or /api/v1 (PLAN §13.2.1). `memory_briefing`
// is the exception Yoga allowed on 2026-09-25, and the reason is that without
// it the Projects tab has no per-project numbers at all: /api/v1/projects
// returns a page count and a date, and `status` has no --project flag, so
// sessions, observations and 7d/30d activity have no other source. The call
// itself is narrow — declared zero-LLM ("WITHOUT any LLM call"), read-only,
// over loopback, one request per project — which is what makes the exception
// affordable. No other MCP tool is called from here.
//
// This is deliberately NOT a full MCP client. ai-memory's streamable-HTTP
// transport answers a bare `tools/call` POST without an initialize handshake
// or a session id (verified against 2.4.0 on 2026-09-25), so one POST is the
// whole protocol surface wick needs.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/agents/agentmemory"
)

// mcpPath is the transport endpoint — the same URL spawned agents are pointed
// at, so the panel reads exactly what the agents write.
const mcpPath = "/mcp"

// briefingRecentPages is how many recent pages one briefing carries. The
// detail panel shows a short list, not a page browser — that is the Wiki
// tab's job — and the backend caps this at 100 anyway.
const briefingRecentPages = 10

// ProjectBriefing reads one project's counters via `memory_briefing`.
//
// The scope is passed EXPLICITLY and is required. ai-memory resolves an
// unnamed scope from the calling process's cwd, and a plain HTTP POST carries
// no cwd — two different directories return the identical answer (verified
// 2026-09-25). So a briefing without workspace+project would silently describe
// whichever project the daemon fell back to, which is a wrong number that
// looks right. The tool's own schema says the same thing: "Static MCP clients
// must pass both."
func (s source) ProjectBriefing(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope) (*agentmemory.ProjectBriefing, error) {
	ws, proj := strings.TrimSpace(sc.Workspace), strings.TrimSpace(sc.Project)
	if ws == "" || proj == "" {
		return nil, fmt.Errorf("%s briefing needs both a workspace and a project", binName)
	}
	var out agentmemory.ProjectBriefing
	if err := callTool(ctx, conn, "memory_briefing", map[string]any{
		"workspace":          ws,
		"project":            proj,
		"recent_pages_limit": briefingRecentPages,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// callTool performs one JSON-RPC tools/call and decodes the tool's payload.
//
// Two unmarshals, not one: JSON-RPC carries the result, and the payload
// itself arrives as a JSON document in a STRING inside result.content[0].text
// — that is how MCP returns structured content over a text channel.
func callTool(ctx context.Context, conn agentmemory.Conn, name string, args map[string]any, v any) error {
	ctx, cancel := context.WithTimeout(ctx, restTimeout)
	defer cancel()

	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  rpcCallParams{Name: name, Arguments: args},
	})
	if err != nil {
		return err
	}
	u := strings.TrimRight(conn.BaseURL, "/") + mcpPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Both types are advertised because the streamable-HTTP transport may
	// answer either; ai-memory answers application/json, and readRPCBody
	// handles the event-stream form rather than trusting that to stay true.
	req.Header.Set("Accept", "application/json, text/event-stream")
	if conn.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+conn.AuthToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRESTBody))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: %s: %s", binName, name, resp.Status, strings.TrimSpace(string(raw)))
	}
	return decodeToolResult(raw, name, v)
}

// decodeToolResult unwraps the two layers and reports a tool failure as an
// error rather than as an empty payload.
//
// There are two ways this call can fail and both must surface: a JSON-RPC
// `error` (an unknown project answers -32602 "project 'x' not found in
// workspace 'default'"), and an `isError: true` result whose text is the
// message. Treating either as "no data" would put a zero in a column that was
// never measured.
func decodeToolResult(raw []byte, name string, v any) error {
	var rpc rpcResponse
	if err := json.Unmarshal(readRPCBody(raw), &rpc); err != nil {
		return fmt.Errorf("parse %s %s: %w", binName, name, err)
	}
	if rpc.Error != nil {
		return fmt.Errorf("%s %s: %s", binName, name, rpc.Error.Message)
	}
	text := firstText(rpc.Result.Content)
	if rpc.Result.IsError {
		if text == "" {
			text = "tool reported an error with no message"
		}
		return fmt.Errorf("%s %s: %s", binName, name, text)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%s %s: empty result", binName, name)
	}
	if err := json.Unmarshal([]byte(text), v); err != nil {
		return fmt.Errorf("parse %s %s payload: %w", binName, name, err)
	}
	return nil
}

// readRPCBody returns the JSON document out of a reply that may have arrived
// as Server-Sent Events. A plain JSON body is handed back untouched; an
// event stream is reduced to its last `data:` payload, which is the one
// carrying the response to this request.
func readRPCBody(raw []byte) []byte {
	b := bytes.TrimSpace(raw)
	if len(b) > 0 && (b[0] == '{' || b[0] == '[') {
		return b
	}
	var last []byte
	for _, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if after, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			last = bytes.TrimSpace(after)
		}
	}
	if len(last) > 0 {
		return last
	}
	return b
}

// firstText returns the first text block of a tool result. ai-memory returns
// exactly one; a backend that splits its document across blocks would need
// them joined, which is not a shape this has ever been seen to produce.
func firstText(blocks []rpcContent) string {
	for _, b := range blocks {
		if b.Text != "" {
			return b.Text
		}
	}
	return ""
}

// ── JSON-RPC envelopes ───────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  rpcCallParams `json:"params"`
}

type rpcCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type rpcResponse struct {
	Result rpcResult `json:"result"`
	Error  *rpcError `json:"error,omitempty"`
}

type rpcResult struct {
	Content []rpcContent `json:"content"`
	IsError bool         `json:"isError"`
}

type rpcContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
