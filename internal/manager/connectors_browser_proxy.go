package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// Live-browser panel proxy.
//
// A playwright_browser live session is a detached Chromium with a CDP endpoint
// on 127.0.0.1:<port> on THIS host. The panel needs a DevTools WebSocket to that
// endpoint for screencast + input. The plugin boundary is unary gRPC, so we:
//
//  1. call the plugin's session_endpoints op to learn the loopback ws URL, then
//  2. dial it from core and proxy the raw CDP WebSocket to the browser client.
//
// Core is the only thing that ever touches the unauthenticated loopback CDP port;
// the browser client only ever talks to this same-origin route, gated per-row by
// canConfigureRow (admin → owner tag → AllowOthersConfigure).

// browserProxyKey is the only connector key allowed through the live-browser
// proxy. The CDP port has no auth, so we never dial it for an arbitrary key.
const browserProxyKey = "playwright_browser"

// browserWSUpgrader upgrades the client side of the proxy. CheckOrigin returns
// true (like the tty WS): the route is already auth-gated + canConfigureRow-
// gated, and the default origin check trips on reverse-proxy / port setups where
// the Origin host doesn't literally equal the request Host.
var browserWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// endpointsTab mirrors one entry of the plugin's session_endpoints output.
type endpointsTab struct {
	Index         int    `json:"index"`
	TargetID      string `json:"target_id"`
	WSDebuggerURL string `json:"ws_debugger_url"`
	URL           string `json:"url"`
	Title         string `json:"title"`
}

type endpointsResult struct {
	SessionID string         `json:"session_id"`
	CDPURL    string         `json:"cdp_url"`
	Tabs      []endpointsTab `json:"tabs"`
	Count     int            `json:"count"`
}

// pickTabWS returns the WebSocket debugger URL for the tab at the given index,
// or "" when no tab has that index. Index is the plugin-assigned page index, not
// the slice position — the two match today, but matching on Index keeps it
// correct if page targets are ever filtered or reordered.
func pickTabWS(tabs []endpointsTab, index int) string {
	for _, t := range tabs {
		if t.Index == index {
			return t.WSDebuggerURL
		}
	}
	return ""
}

// resolveBrowserSession loads the connector row, enforces access + the
// playwright_browser key, and returns the row so the caller can execute ops.
// Writes the error response itself and returns ok=false on any failure.
func (h *Handler) resolveBrowserSession(w http.ResponseWriter, r *http.Request) (*entity.Connector, bool) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	id := r.PathValue("id")

	if key != browserProxyKey {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "live browser view is only available for " + browserProxyKey})
		return nil, false
	}
	row, err := h.connectors.Get(ctx, id)
	if err != nil || row.Key != key || !h.canSeeRow(r, user, row.ID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "connector not found"})
		return nil, false
	}
	// Driving a live browser is at least as privileged as editing the row's
	// credentials, so reuse the same gate (admin → owner tag → AllowOthers).
	if !h.canConfigureRow(user, row) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you don't have access to this connector's live browser"})
		return nil, false
	}
	return row, true
}

// execBrowserOp runs one playwright_browser op and unmarshals its JSON result
// into out. Returns a user-facing error string (empty on success).
func (h *Handler) execBrowserOp(ctx context.Context, user *entity.User, r *http.Request, rowID, op string, input map[string]string, out any) string {
	res, execErr := h.connectors.Execute(ctx, connectors.ExecuteParams{
		ConnectorID:  rowID,
		OperationKey: op,
		Input:        input,
		Source:       entity.ConnectorRunSourceTest,
		UserID:       userID(user),
		IPAddress:    clientIP(r),
		UserAgent:    r.UserAgent(),
	})
	if execErr != nil && (res == nil || res.ErrorMessage == "") {
		return execErr.Error()
	}
	if res == nil {
		return "no result from " + op
	}
	if res.ErrorMessage != "" {
		return res.ErrorMessage
	}
	if out != nil {
		if err := json.Unmarshal([]byte(orEmptyJSON(res.ResponseJSON)), out); err != nil {
			return "decode " + op + " response: " + err.Error()
		}
	}
	return ""
}

// apiBrowserOpen opens a live session (session_open op) and returns its id.
func (h *Handler) apiBrowserOpen(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveBrowserSession(w, r)
	if !ok {
		return
	}
	var out map[string]any
	if errMsg := h.execBrowserOp(r.Context(), login.GetUser(r.Context()), r, row.ID, "session_open", map[string]string{}, &out); errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": errMsg})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// apiBrowserClose closes a live session (session_close op).
func (h *Handler) apiBrowserClose(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveBrowserSession(w, r)
	if !ok {
		return
	}
	sid := r.PathValue("session")
	if sid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id required"})
		return
	}
	// Abort every in-flight op bound to this session BEFORE killing the browser,
	// so no run keeps executing against a browser that's about to be gone (which
	// left runs hung in "running" and could crash the driver with EPIPE). The
	// aborted ops' contexts unblock their plugin calls and finalize as
	// "cancelled". Done first so the session_close op below (which registers
	// under the same session id) isn't itself swept by this cancel.
	h.connectors.CancelSession(sid)
	var out map[string]any
	if errMsg := h.execBrowserOp(r.Context(), login.GetUser(r.Context()), r, row.ID, "session_close", map[string]string{"session_id": sid}, &out); errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": errMsg})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// apiBrowserTabNew opens a new tab in a session (tab_new op). Body: {"url": ""}.
func (h *Handler) apiBrowserTabNew(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveBrowserSession(w, r)
	if !ok {
		return
	}
	sid := r.PathValue("session")
	if sid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id required"})
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var out map[string]any
	if errMsg := h.execBrowserOp(r.Context(), login.GetUser(r.Context()), r, row.ID, "tab_new", map[string]string{
		"session_id": sid,
		"url":        strings.TrimSpace(body.URL),
	}, &out); errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": errMsg})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// apiBrowserTabClose closes a tab by index in a session (tab_close op).
func (h *Handler) apiBrowserTabClose(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveBrowserSession(w, r)
	if !ok {
		return
	}
	sid := r.PathValue("session")
	idx := r.PathValue("index")
	if sid == "" || idx == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id and tab index required"})
		return
	}
	var out map[string]any
	if errMsg := h.execBrowserOp(r.Context(), login.GetUser(r.Context()), r, row.ID, "tab_close", map[string]string{
		"session_id": sid,
		"index":      idx,
	}, &out); errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": errMsg})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// apiBrowserSessions lists live sessions (session_list op) for the dropdown.
func (h *Handler) apiBrowserSessions(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveBrowserSession(w, r)
	if !ok {
		return
	}
	var out map[string]any
	if errMsg := h.execBrowserOp(r.Context(), login.GetUser(r.Context()), r, row.ID, "session_list", map[string]string{}, &out); errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": errMsg})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// apiBrowserWS proxies a DevTools WebSocket between the panel and the live
// session's CDP endpoint. Flow: resolve endpoints (session_endpoints op) → pick
// the requested tab → dial the loopback ws → pump frames both ways until either
// side closes. Gated by canConfigureRow; the raw CDP port never leaves core.
func (h *Handler) apiBrowserWS(w http.ResponseWriter, r *http.Request) {
	row, ok := h.resolveBrowserSession(w, r)
	if !ok {
		return
	}
	sid := r.URL.Query().Get("session")
	if sid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session query param required"})
		return
	}
	tabIdx := 0
	if t := r.URL.Query().Get("tab"); t != "" {
		if n, err := strconv.Atoi(t); err == nil && n >= 0 {
			tabIdx = n
		}
	}

	// Resolve the CDP ws URL for the requested tab BEFORE upgrading — an error
	// here should be a plain HTTP response, not a WS close.
	var eps endpointsResult
	if errMsg := h.execBrowserOp(r.Context(), login.GetUser(r.Context()), r, row.ID, "session_endpoints", map[string]string{"session_id": sid}, &eps); errMsg != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": errMsg})
		return
	}
	wsURL := pickTabWS(eps.Tabs, tabIdx)
	if wsURL == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no debuggable tab at index " + strconv.Itoa(tabIdx)})
		return
	}

	mode := panelMode(r.URL.Query().Get("mode"))
	l := log.With().Str("component", "browser-proxy").Str("connector", row.ID).Str("session", sid).Int("tab", tabIdx).Str("mode", mode).Logger()

	// Some reverse proxies rewrite Connection: to "keep-alive", stripping the
	// "upgrade" token so gorilla's Upgrade() rejects an otherwise-valid handshake.
	// Restore it when this is clearly a WS request (same fix as the tty proxy).
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		!strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		r.Header.Set("Connection", "Upgrade")
	}

	// Dial the loopback CDP endpoint (core → 127.0.0.1:<port>). Chrome's DevTools
	// endpoint rejects a handshake carrying a non-localhost Origin, so send none.
	upstream, resp, err := websocket.DefaultDialer.DialContext(r.Context(), wsURL, http.Header{})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		l.Warn().Err(err).Str("ws_url", wsURL).Int("cdp_status", status).Msg("dial CDP endpoint failed")
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach the live browser (it may have been closed)"})
		return
	}
	defer upstream.Close()

	client, err := browserWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		l.Warn().Err(err).Msg("client ws upgrade failed")
		return
	}
	defer client.Close()

	l.Debug().Msg("browser proxy connected")
	proxyWebSocket(client, upstream, func(msg []byte) []byte { return cdpGate(mode, msg) })
	l.Debug().Msg("browser proxy closed")
}

// panelMode normalises the panel's mode query. Anything but "full" (an older
// tab that sends none included) is watch-only.
func panelMode(q string) string {
	if q == "full" {
		return "full"
	}
	return "view"
}

// cdpGate decides whether one client → browser CDP message may pass. It
// returns nil to forward the message, or the error reply to send back to the
// client instead.
//
// The live panel shows the agent's own browser, and opening it must not
// change what the agent sees or interrupt what it is doing. So in every mode
// the panel may only stream (screencast, acks, the "1" heartbeat), and never
// emulate: Emulation.* would resize the viewport or swap the colour scheme
// under the agent and move every coordinate it measured. Full mode adds what a
// person taking over needs: input, navigation and page scripts (clipboard
// sync). Everything else is refused here, so a stale or modified client
// cannot do more than its mode allows.
func cdpGate(mode string, raw []byte) []byte {
	var msg struct {
		ID     *int64          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil || msg.Method == "" {
		return cdpRefusal(msg.ID, "malformed CDP message")
	}
	switch msg.Method {
	case "Page.enable", "Page.startScreencast", "Page.stopScreencast", "Page.screencastFrameAck":
		return nil
	case "Runtime.evaluate":
		if mode == "full" || isHeartbeat(msg.Params) {
			return nil
		}
	case "Page.navigate":
		if mode == "full" {
			return nil
		}
	default:
		if mode == "full" && strings.HasPrefix(msg.Method, "Input.") {
			return nil
		}
	}
	return cdpRefusal(msg.ID, msg.Method+" is not allowed in "+mode+" mode: the panel must not change the agent's browser")
}

// isHeartbeat reports a Runtime.evaluate of the literal "1" (the panel's
// liveness ping), which reads nothing and changes nothing.
func isHeartbeat(params json.RawMessage) bool {
	var p struct {
		Expression string `json:"expression"`
	}
	return json.Unmarshal(params, &p) == nil && strings.TrimSpace(p.Expression) == "1"
}

// cdpRefusal is the CDP error reply for a refused command. A message without
// an id gets no reply (nothing on the client waits for one).
func cdpRefusal(id *int64, reason string) []byte {
	if id == nil {
		return []byte{}
	}
	b, _ := json.Marshal(map[string]any{"id": *id, "error": map[string]any{"code": -32000, "message": reason}})
	return b
}

// proxyWebSocket pumps messages in both directions until either side closes.
// Each direction runs in its own goroutine; the first to finish forces the other
// pump's read to error via a past read deadline. gate, when set, vets each
// client → upstream message: nil forwards it, anything else is not forwarded
// and (when non-empty) is written back to the client as the reply.
func proxyWebSocket(client, upstream *websocket.Conn, gate func([]byte) []byte) {
	done := make(chan struct{}, 2)
	var clientMu sync.Mutex // the client conn is written by both pumps
	writeClient := func(mt int, msg []byte) error {
		clientMu.Lock()
		defer clientMu.Unlock()
		return client.WriteMessage(mt, msg)
	}
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			mt, msg, err := client.ReadMessage()
			if err != nil {
				return
			}
			if gate != nil {
				if reply := gate(msg); reply != nil {
					if len(reply) > 0 {
						if err := writeClient(websocket.TextMessage, reply); err != nil {
							return
						}
					}
					continue
				}
			}
			if err := upstream.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			mt, msg, err := upstream.ReadMessage()
			if err != nil {
				return
			}
			if err := writeClient(mt, msg); err != nil {
				return
			}
		}
	}()

	<-done
	// Unblock the other pump: closing both conns forces its ReadMessage to error.
	_ = client.SetReadDeadline(time.Now())
	_ = upstream.SetReadDeadline(time.Now())
	<-done
}
