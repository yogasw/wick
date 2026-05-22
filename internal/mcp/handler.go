package mcp

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/yogasw/wick/internal/agents/askuser"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentpool "github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp/handlers"
)

// supportedProtocolVersions lists every MCP revision this server can speak,
// newest first. latestProtocolVersion is returned when the client requests
// one we don't support, per spec §lifecycle.
var supportedProtocolVersions = []string{"2025-03-26", "2024-11-05"}

const latestProtocolVersion = "2025-03-26"

// Handler wires the MCP JSON-RPC surface — tools/list, tools/call, initialize.
// Bearer auth is applied by AuthMiddleware before this handler runs.
//
// Tool surface uses a meta-tool pattern: three stable tools (wick_list,
// wick_search, wick_execute) let the LLM discover connectors at runtime.
// Adding/removing connectors never changes the cached tool list.
type Handler struct {
	connectors *connectors.Service
	version    string
	commit     string
	buildTime  string
	wickRoot   string // project root; set for CLI (stdio), empty for HTTP
	// appURL returns the live base URL at request time. Used to build
	// absolute redirect links for wick_encrypt / wick_decrypt.
	appURL func() string
	// askUsers handles the ask_user MCP tool. nil = tool returns an error.
	askUsers *askuser.Manager
	// askUserAllowed is the gate-policy check for ask_user calls.
	// nil = always allowed (stdio / tests).
	askUserAllowed func() (bool, string)
	// pool is wired for wick_switch_provider and wick_kill_session.
	// nil in stdio mode and tests.
	pool   *agentpool.Pool
	layout agentconfig.Layout
}

func NewHandler(c *connectors.Service) *Handler {
	return &Handler{connectors: c, version: "dev", commit: "", buildTime: "unknown"}
}

func (h *Handler) WithAppURL(get func() string) *Handler {
	h.appURL = get
	return h
}

func (h *Handler) WithWickRoot(root string) *Handler {
	h.wickRoot = root
	return h
}

func (h *Handler) WithAskUser(m *askuser.Manager) *Handler {
	h.askUsers = m
	return h
}

func (h *Handler) WithAskUserPolicy(fn func() (bool, string)) *Handler {
	h.askUserAllowed = fn
	return h
}

func (h *Handler) WithPool(p *agentpool.Pool, layout agentconfig.Layout) *Handler {
	h.pool = p
	h.layout = layout
	return h
}

func (h *Handler) WithBuildInfo(version, commit, buildTime string) *Handler {
	h.version = version
	if len(commit) > 8 {
		commit = commit[:8]
	}
	h.commit = commit
	h.buildTime = buildTime
	return h
}

func (h *Handler) serverVersion() string {
	if h.commit != "" && h.commit != "dev" {
		return h.version + " (" + h.commit + ")"
	}
	return h.version
}

// responder builds the Responder the handlers subpackage uses to write
// JSON-RPC responses without importing the unexported write helpers.
func (h *Handler) responder() handlers.Responder {
	return handlers.Responder{
		WriteResult: writeRPCResult,
		WriteError:  writeRPCError,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeRPCError(w, nil, errParseError, "could not read request", nil)
		return
	}
	defer r.Body.Close()

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, nil, errParseError, "invalid JSON", nil)
		return
	}
	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, errInvalidRequest, "jsonrpc must be 2.0", nil)
		return
	}
	if req.isNotification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Streamable HTTP (spec 2025-03-26): SSE for tools/call when client accepts it.
	if req.Method == "tools/call" && wantsSSE(r) {
		h.handleToolsCallSSE(w, r, req)
		return
	}

	switch req.Method {
	case "initialize":
		h.handleInitialize(w, r, req)
	case "tools/list":
		h.handleToolsList(w, r, req)
	case "tools/call":
		h.handleToolsCall(w, r, req)
	case "ping":
		writeRPCResult(w, req.ID, struct{}{})
	default:
		writeRPCError(w, req.ID, errMethodNotFound, "unknown method: "+req.Method, nil)
	}
}

// ── initialize ───────────────────────────────────────────────────────

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      serverInfo         `json:"serverInfo"`
	Capabilities    serverCapabilities `json:"capabilities"`
	Instructions    string             `json:"instructions,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type serverCapabilities struct {
	Tools toolsCapability `json:"tools"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

func (h *Handler) handleInitialize(w http.ResponseWriter, _ *http.Request, req rpcRequest) {
	var p initializeParams
	_ = json.Unmarshal(req.Params, &p)
	writeRPCResult(w, req.ID, initializeResult{
		ProtocolVersion: negotiateProtocolVersion(p.ProtocolVersion),
		ServerInfo:      serverInfo{Name: "wick", Version: h.serverVersion()},
		Capabilities:    serverCapabilities{Tools: toolsCapability{ListChanged: false}},
		Instructions:    serverInstructions,
	})
}

func negotiateProtocolVersion(requested string) string {
	for _, v := range supportedProtocolVersions {
		if v == requested {
			return v
		}
	}
	return latestProtocolVersion
}

// ── tools/list ───────────────────────────────────────────────────────

func (h *Handler) handleToolsList(w http.ResponseWriter, _ *http.Request, req rpcRequest) {
	writeRPCResult(w, req.ID, handlers.ToolListResult{Tools: handlers.MetaToolDescriptors()})
}

// ── tools/call ───────────────────────────────────────────────────────

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Meta      *callMeta      `json:"_meta,omitempty"`
}

type callMeta struct {
	ProgressToken json.RawMessage `json:"progressToken,omitempty"`
}

// type aliases for test files in this package.
type listResult = handlers.ListResult
type toolListResult = handlers.ToolListResult

func (h *Handler) handleToolsCall(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	user := login.GetUser(r.Context())
	if user == nil {
		writeRPCError(w, req.ID, errInternal, "no authenticated user on context", nil)
		return
	}
	tagIDs := login.GetUserTagIDs(r.Context())

	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, errInvalidParams, "invalid params: "+err.Error(), nil)
		return
	}

	rsp := h.responder()
	hreq := handlers.RPCRequest{ID: req.ID, Params: req.Params}

	switch p.Name {
	case "wick_list":
		handlers.WickList(w, r, hreq, rsp, h.connectors, tagIDs, user.IsAdmin())
	case "wick_search":
		handlers.WickSearch(w, r, hreq, rsp, h.connectors, p.Arguments, tagIDs, user.IsAdmin())
	case "wick_get":
		handlers.WickGet(w, r, hreq, rsp, h.connectors, p.Arguments, tagIDs, user.IsAdmin())
	case "wick_execute":
		handlers.WickExecute(w, r, hreq, rsp, h.connectors, p.Arguments, user, tagIDs)
	case "wick_info":
		handlers.WickInfo(w, hreq, rsp, h.version, h.commit, h.buildTime, h.wickRoot)
	case "wick_encrypt":
		handlers.WickEncrypt(w, hreq, rsp, func(s string) string { return handlers.EncfieldsURL(h.appURL, s) })
	case "wick_decrypt":
		handlers.WickDecrypt(w, hreq, rsp, func(s string) string { return handlers.EncfieldsURL(h.appURL, s) })
	case "ask_user":
		handlers.AskUser(w, r, hreq, rsp, h.askUsers, h.askUserAllowed, p.Arguments)
	case "wick_list_providers":
		handlers.WickListProviders(w, hreq, rsp, h.layout, p.Arguments)
	case "wick_skill_list":
		handlers.WickSkillList(w, hreq, rsp)
	case "wick_skill_sync":
		handlers.WickSkillSync(w, hreq, rsp)
	default:
		writeRPCError(w, req.ID, errInvalidParams, "unknown tool: "+p.Name, nil)
	}
}

