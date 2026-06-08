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
	matched := h.Router.Dispatch(context.Background(), evt)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"matched":%d}`, matched)
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
	// strippedPath = /{wf_id}/{slug} or /{wf_id}
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
	// Optional HMAC: look up secret from the draft's matching trigger.
	if h.SecretLookup != nil {
		for _, tr := range draft.Triggers {
			if tr.Type != workflow.TriggerWebhook || tr.SecretRef == "" {
				continue
			}
			full := webhookFullPath(wfID, tr.Path)
			if PathMatches(full, strippedPath) {
				if !verifyWebhookSecret(w, body, tr.SecretRef, r.Header.Get("X-Wick-Sig"), h.SecretLookup) {
					return
				}
				break
			}
		}
	}
	evt.Payload["draft"] = true
	evt.Payload["source"] = "webhook-test"
	if err := h.Router.RunNowWith(context.Background(), wfID, &draft, evt); err != nil {
		http.Error(w, "dispatch failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"matched":1,"draft":true}`)
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
	body, err = io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
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
