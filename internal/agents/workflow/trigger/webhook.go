package trigger

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// respondTimeout caps how long a blocking webhook handler (respond_mode =
// last_node or respond_node) waits for the workflow to complete before
// returning HTTP 504 Gateway Timeout to the caller.
//
// 30s is chosen as a safe upper bound:
//   - Long enough for most I/O-bound workflows (HTTP calls, DB queries).
//   - Short enough that reverse-proxies and load-balancers (nginx default
//     60s, AWS ALB 60s) don't kill the connection first.
//
// If your workflow legitimately takes longer, use respond_mode = "immediately"
// and poll for the result via the run-status API instead.
//
// Var (not const) so tests can set a short value without sleeping.
var respondTimeout = 30 * time.Second

// DraftLoader loads the draft copy of a workflow by ID. Satisfied by
// service.Service — injected so the trigger package stays free of the
// service import cycle.
type DraftLoader interface {
	LoadDraft(id string) (workflow.Workflow, error)
}

// WebhookHandler turns inbound HTTP POSTs into Events and dispatches
// them via the Router against the **published** workflow copy.
// Mount at `/webhook/` on the wick HTTP server.
type WebhookHandler struct {
	Router       *Router
	SecretLookup func(secretRef string) (string, error)
}

// NewWebhookHandler builds a published-workflow webhook handler.
func NewWebhookHandler(r *Router) *WebhookHandler {
	return &WebhookHandler{Router: r}
}

// ServeHTTP parses the request and dispatches to the published workflow.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	strippedPath, body, evt, ok := parseWebhookRequest(w, r, "/webhook")
	if !ok {
		return
	}
	if secretRef, ok2 := h.Router.WebhookSecretFor(strippedPath); ok2 && h.SecretLookup != nil {
		if !verifyWebhookSecret(w, body, secretRef, r.Header.Get("X-Wick-Sig"), h.SecretLookup) {
			return
		}
	}
	// Resolve respond_mode from the matching trigger.
	mode := h.Router.respondModeFor(strippedPath, evt)
	if mode == "" || mode == workflow.RespondModeImmediately {
		matched := h.Router.Dispatch(context.Background(), evt)
		if matched == 0 {
			http.Error(w, "no webhook trigger matches this path", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"matched":%d}`, matched)
		return
	}
	// Blocking modes: dispatch with Done channels and wait.
	dones := h.Router.DispatchWithDone(context.Background(), evt)
	if len(dones) == 0 {
		http.Error(w, "no webhook trigger matches this path", http.StatusNotFound)
		return
	}
	writeWebhookResult(w, dones[0], mode)
}

// DraftWebhookHandler dispatches inbound POSTs against the **draft**
// workflow copy so operators can test changes without publishing.
// Mount at `/webhook-test/` on the wick HTTP server.
//
// Path format: /webhook-test/{wf_id}/{slug}
// The wf_id segment is extracted from the path to load the right draft.
type DraftWebhookHandler struct {
	Router       *Router
	Drafts       DraftLoader
	SecretLookup func(secretRef string) (string, error)
}

// NewDraftWebhookHandler builds a draft-mode webhook handler.
func NewDraftWebhookHandler(r *Router, drafts DraftLoader) *DraftWebhookHandler {
	return &DraftWebhookHandler{Router: r, Drafts: drafts}
}

// ServeHTTP loads the draft copy and fires it via RunNowWith.
func (h *DraftWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	strippedPath, body, evt, ok := parseWebhookRequest(w, r, "/webhook-test")
	if !ok {
		return
	}
	// Extract wf_id — first non-empty segment of the stripped path.
	parts := strings.SplitN(strings.TrimPrefix(strippedPath, "/"), "/", 2)
	wfID := parts[0]
	if wfID == "" {
		http.Error(w, "missing workflow id in path", http.StatusBadRequest)
		return
	}
	draft, err := h.Drafts.LoadDraft(wfID)
	if err != nil {
		http.Error(w, "draft not found: "+err.Error(), http.StatusNotFound)
		return
	}
	// Find the matching webhook trigger by path. No match = 404.
	var mode string
	matched := false
	for _, tr := range draft.Triggers {
		if tr.Type != workflow.TriggerWebhook {
			continue
		}
		full := webhookFullPath(wfID, tr.Path)
		if !PathMatches(full, strippedPath) {
			continue
		}
		matched = true
		mode = tr.RespondMode
		if tr.Method != "" && !strings.EqualFold(tr.Method, r.Method) {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if tr.SecretRef != "" && h.SecretLookup != nil {
			if !verifyWebhookSecret(w, body, tr.SecretRef, r.Header.Get("X-Wick-Sig"), h.SecretLookup) {
				return
			}
		}
		break
	}
	if !matched {
		http.Error(w, "no webhook trigger matches this path", http.StatusNotFound)
		return
	}
	evt.Payload["draft"] = true
	evt.Payload["source"] = "webhook-test"
	if mode == "" || mode == workflow.RespondModeImmediately {
		if err := h.Router.RunNowWith(context.Background(), wfID, &draft, evt); err != nil {
			http.Error(w, "dispatch failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"matched":1,"draft":true}`)
		return
	}
	done, err := h.Router.RunNowWithDone(context.Background(), wfID, &draft, evt)
	if err != nil {
		http.Error(w, "dispatch failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeWebhookResult(w, done, mode)
}

// writeWebhookResult waits for a run to complete and writes the HTTP
// response according to mode. Handles last_node and respond_node.
func writeWebhookResult(w http.ResponseWriter, done <-chan RunResult, mode string) {
	ctx, cancel := context.WithTimeout(context.Background(), respondTimeout)
	defer cancel()
	var result RunResult
	select {
	case result = <-done:
	case <-ctx.Done():
		http.Error(w, "webhook response timeout — workflow did not finish in time", http.StatusGatewayTimeout)
		return
	}
	if result.Err != nil {
		http.Error(w, result.Err.Error(), http.StatusInternalServerError)
		return
	}
	st := result.State
	switch mode {
	case workflow.RespondModeLastNode:
		// Return the last completed node's output as JSON.
		var lastOut any
		for _, nodeID := range st.Completed {
			if out, ok := st.Outputs[nodeID]; ok {
				lastOut = out
			}
		}
		writeJSONResponse(w, http.StatusOK, lastOut)
	case workflow.RespondModeRespondNode:
		status, body, headers, found := extractRespondNodeOutput(st)
		if !found {
			// Workflow completed but no webhook_respond node ran.
			// Run is persisted; only the HTTP response is affected.
			http.Error(w, "workflow completed but no webhook_respond node produced a response", http.StatusBadGateway)
			return
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		// Auto content-type: JSON when body looks like JSON, else text/plain.
		if w.Header().Get("Content-Type") == "" {
			trimmed := strings.TrimSpace(body)
			if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
				w.Header().Set("Content-Type", "application/json")
			} else {
				w.Header().Set("Content-Type", "text/plain")
			}
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	default:
		writeJSONResponse(w, http.StatusOK, st.Outputs)
	}
}

// extractRespondNodeOutput scans RunState.Outputs for the first
// webhook_respond node (identified by the "_webhook_respond" sentinel
// key) and returns its status, body, headers. Falls back to 200 + empty
// when no such node ran.
func extractRespondNodeOutput(st workflow.RunState) (status int, body string, headers map[string]string, found bool) {
	for _, nodeID := range st.Completed {
		out, ok := st.Outputs[nodeID]
		if !ok {
			continue
		}
		outMap, ok := out.(map[string]any)
		if !ok {
			continue
		}
		if flag, _ := outMap["_webhook_respond"].(bool); !flag {
			continue
		}
		statusInt := http.StatusOK
		switch s := outMap["status"].(type) {
		case int:
			statusInt = s
		case float64:
			statusInt = int(s)
		}
		bodyStr, _ := outMap["body"].(string)
		hdrs := map[string]string{}
		if hMap, ok := outMap["headers"].(map[string]any); ok {
			for k, v := range hMap {
				if vs, ok := v.(string); ok {
					hdrs[k] = vs
				}
			}
		} else if hMap, ok := outMap["headers"].(map[string]string); ok {
			hdrs = hMap
		}
		return statusInt, bodyStr, hdrs, true
	}
	return http.StatusOK, "", nil, false
}

// writeJSONResponse serialises v as JSON and writes it with status code.
func writeJSONResponse(w http.ResponseWriter, code int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
}

// parseWebhookRequest reads the body + builds the Event payload.
// mountPrefix is "/webhook" or "/webhook-test" — stripped from the path
// before indexing so the router key is always /{wf_id}/{slug}.
// Returns ok=false when it already wrote an error response.
func parseWebhookRequest(w http.ResponseWriter, r *http.Request, mountPrefix string) (strippedPath string, body []byte, evt workflow.Event, ok bool) {
	if !strings.HasPrefix(r.URL.Path, mountPrefix+"/") {
		http.NotFound(w, r)
		return
	}
	var err error
	const maxWebhookBody = 10 << 20 // 10 MiB — guard against unbounded external payloads
	body, err = io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) > maxWebhookBody {
		http.Error(w, "request body exceeds 10MiB limit", http.StatusRequestEntityTooLarge)
		return
	}
	_ = r.Body.Close()

	strippedPath = strings.TrimPrefix(r.URL.Path, mountPrefix)
	if strippedPath == "" {
		strippedPath = "/"
	}

	headers := map[string]string{}
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	payload := map[string]any{
		"path":    strippedPath,
		"method":  r.Method,
		"headers": headers,
		"query":   flattenURLValues(r.URL.Query()),
	}
	parsedBody, parseErr := parseWebhookBody(r.Header.Get("Content-Type"), body)
	if parseErr == nil {
		payload["body"] = parsedBody
	} else {
		payload["body_raw"] = string(body)
	}
	payload["raw"] = body

	evt = workflow.Event{
		Type:    string(workflow.TriggerWebhook),
		At:      time.Now().UTC(),
		Payload: payload,
	}
	ok = true
	return
}

// verifyWebhookSecret checks HMAC and writes 401 on failure.
func verifyWebhookSecret(w http.ResponseWriter, body []byte, secretRef, sig string, lookup func(string) (string, error)) bool {
	secret, err := lookup(secretRef)
	if err != nil {
		http.Error(w, "secret lookup failed", http.StatusInternalServerError)
		return false
	}
	if sig == "" || !VerifyHMAC(body, secret, sig) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return false
	}
	return true
}

// VerifyHMAC computes SHA-256 HMAC and constant-time compares.
func VerifyHMAC(body []byte, secret, want string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	got := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(got), []byte(want))
}

func parseWebhookBody(contentType string, body []byte) (any, error) {
	contentType = strings.ToLower(contentType)
	if strings.Contains(contentType, "application/json") {
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			return nil, err
		}
		return v, nil
	}
	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		out := map[string]string{}
		for _, pair := range strings.Split(string(body), "&") {
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 2 {
				out[parts[0]] = parts[1]
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported content type %q", contentType)
}

func flattenURLValues(v map[string][]string) map[string]string {
	out := map[string]string{}
	for k, vs := range v {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}
