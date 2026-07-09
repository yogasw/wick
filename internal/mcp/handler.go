package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/askuser"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentpool "github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp/handlers"
	"gorm.io/gorm"
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
	// askUsers handles the ask_user MCP tool (and wick_session_workspace
	// configure/add modals). In the HTTP server it's the in-process
	// askuser.Manager; in stdio mode it's an askuser.SocketAsker that
	// forwards to the running server over the askuser unix socket.
	// nil = tool returns an error.
	askUsers askuser.Asker
	// askUserAllowed is the policy check for ask_user / wick_session_workspace
	// asks, resolved per session origin (see api.askUserPolicy). The
	// argument is the request's session_id. nil = always allowed
	// (tests).
	askUserAllowed func(sessionID string) (bool, string)
	// pool is wired for wick_switch_provider and wick_kill_session.
	// nil in stdio mode and tests.
	pool   *agentpool.Pool
	layout agentconfig.Layout
	// refreshSession reloads one session into the in-memory registry
	// after a handler mutates its meta on disk (wick_set_title). nil in
	// stdio mode and tests — the disk write still lands; only the live
	// dashboard cache misses the update until the next reload.
	refreshSession func(id string) error
	// db is passed to wick_info so it can surface DB status/type.
	// May be nil (tests, smoke mode); handlers.WickInfo reports
	// "disabled" in that case. DSN is never exposed.
	db *gorm.DB
	// schedule backs wick_schedule_message (create/list/cancel future
	// message injections). nil in stdio mode and tests — the tool then
	// reports scheduling unavailable.
	schedule *schedule.Store
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

func (h *Handler) WithAskUser(m askuser.Asker) *Handler {
	h.askUsers = m
	return h
}

func (h *Handler) WithAskUserPolicy(fn func(sessionID string) (bool, string)) *Handler {
	h.askUserAllowed = fn
	return h
}

func (h *Handler) WithPool(p *agentpool.Pool, layout agentconfig.Layout) *Handler {
	h.pool = p
	h.layout = layout
	return h
}

func (h *Handler) WithDB(db *gorm.DB) *Handler {
	h.db = db
	return h
}

// WithSchedule wires the scheduled-message store that backs
// wick_schedule_message. nil (stdio/tests) disables the tool.
func (h *Handler) WithSchedule(s *schedule.Store) *Handler {
	h.schedule = s
	return h
}

// WithLayout wires the agents storage layout without a pool — stdio
// mode needs it for the session-scoped tools (wick_session_info,
// wick_set_title, wick_session_workspace) even though no agent pool
// runs in that process.
func (h *Handler) WithLayout(l agentconfig.Layout) *Handler {
	h.layout = l
	return h
}

// WithRefreshSession wires the registry-refresh callback used by
// wick_set_title to keep the dashboard's in-memory session cache in
// sync after the title is written to disk.
func (h *Handler) WithRefreshSession(fn func(id string) error) *Handler {
	h.refreshSession = fn
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
	// Streamable HTTP transport (MCP spec 2025-03-26): POST carries
	// JSON-RPC, GET opens the server→client SSE channel the client needs
	// to complete its handshake, DELETE tears the session down.
	switch r.Method {
	case http.MethodPost:
		h.handlePost(w, r)
	case http.MethodGet:
		h.handleGetStream(w, r)
	case http.MethodDelete:
		// Stateless server — no per-session resources to free. Ack so
		// the client's teardown succeeds instead of seeing a 404/405.
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetStream holds open the server→client SSE channel. wick emits no
// server-initiated messages, so the stream just heartbeats until the
// client disconnects — but its presence is what lets the client's
// Streamable-HTTP handshake finish and register the tools.
func (h *Handler) handleGetStream(w http.ResponseWriter, r *http.Request) {
	if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
		w.Header().Set("Mcp-Session-Id", sid)
	}
	sess, ok := newSSESession(w)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	defer sess.close()
	ticker := time.NewTicker(sseHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			sess.writeKeepalive()
		}
	}
}

// newSessionID mints an opaque id for the Mcp-Session-Id header. The
// server is stateless (each POST is self-contained), so the id is only
// for client-side correlation of its GET stream with its session.
func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "wick"
	}
	return hex.EncodeToString(b)
}

func (h *Handler) handlePost(w http.ResponseWriter, r *http.Request) {
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
	w.Header().Set("Mcp-Session-Id", newSessionID())
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

func (h *Handler) handleToolsList(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	tools := handlers.MetaToolDescriptors()
	if user := login.GetUser(r.Context()); user != nil {
		tagIDs := login.GetUserTagIDs(r.Context())
		tools = append(tools, handlers.WickManagerToolDescriptors(r.Context(), h.connectors, tagIDs, user.IsAdmin())...)
	}
	writeRPCResult(w, req.ID, handlers.ToolListResult{Tools: tools})
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
		handlers.WickList(w, r, hreq, rsp, h.connectors, h.layout, p.Arguments, tagIDs, user.IsAdmin())
	case "wick_search":
		handlers.WickSearch(w, r, hreq, rsp, h.connectors, h.layout, p.Arguments, tagIDs, user.IsAdmin())
	case "wick_get":
		handlers.WickGet(w, r, hreq, rsp, h.connectors, h.layout, p.Arguments, tagIDs, user.IsAdmin())
	case "wick_execute":
		handlers.WickExecute(w, r, hreq, rsp, h.connectors, h.layout, p.Arguments, user, tagIDs)
	case "wick_info":
		handlers.WickInfo(w, hreq, rsp, h.version, h.commit, h.buildTime, h.wickRoot, h.db)
	case "wick_encrypt":
		handlers.WickEncrypt(w, hreq, rsp, func(s string) string { return handlers.EncfieldsURL(h.appURL, s) })
	case "wick_decrypt":
		handlers.WickDecrypt(w, hreq, rsp, func(s string) string { return handlers.EncfieldsURL(h.appURL, s) })
	case "ask_user":
		handlers.AskUser(w, r, hreq, rsp, h.askUsers, h.askUserAllowed, p.Arguments)
	case "wick_session_workspace":
		handlers.WickSessionWorkspace(w, r, hreq, rsp, h.connectors, h.layout, h.askUsers, h.askUserAllowed, p.Arguments, user, tagIDs)
	case "wick_list_providers":
		handlers.WickListProviders(w, hreq, rsp, h.layout, p.Arguments)
	case "wick_skill_list":
		handlers.WickSkillList(w, hreq, rsp)
	case "wick_skill_sync":
		handlers.WickSkillSync(w, r, hreq, rsp)
	case "wick_session_info":
		handlers.WickSessionInfo(w, r, hreq, rsp, h.layout, p.Arguments)
	case "wick_set_title":
		handlers.WickSetTitle(w, r, hreq, rsp, h.layout, h.refreshSession, p.Arguments)
	case "wick_schedule_message":
		handlers.WickScheduleMessage(w, r, hreq, rsp, h.schedule, h.layout, p.Arguments, user)
	default:
		if strings.HasPrefix(p.Name, handlers.WickManagerPrefix) {
			handlers.WickManagerExecute(w, r, hreq, rsp, h.connectors, p.Name, p.Arguments, user, tagIDs)
		} else {
			writeRPCError(w, req.ID, errInvalidParams, "unknown tool: "+p.Name, nil)
		}
	}
}
