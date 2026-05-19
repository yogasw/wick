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

// WebhookHandler turns inbound HTTP POSTs into Events and dispatches
// them via the Router. Mount at `/hooks/` on the wick HTTP server.
type WebhookHandler struct {
	Router       *Router
	SecretLookup func(secretRef string) (string, error)
}

// NewWebhookHandler builds a handler.
func NewWebhookHandler(r *Router) *WebhookHandler {
	return &WebhookHandler{Router: r}
}

// ServeHTTP parses the request and dispatches.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/hooks/") {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	headers := map[string]string{}
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	payload := map[string]any{
		"path":    r.URL.Path,
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

	evt := workflow.Event{
		Type:    string(workflow.TriggerWebhook),
		At:      time.Now().UTC(),
		Payload: payload,
	}

	// HMAC verification: if the matching trigger carries a secret_ref
	// and the request provides X-Wick-Sig, validate before dispatch.
	// Reject outright when a secret_ref is configured but no sig header
	// is present — prevents unsigned calls from triggering guarded workflows.
	if secretRef, ok := h.Router.WebhookSecretFor(r.URL.Path); ok && h.SecretLookup != nil {
		secret, err := h.SecretLookup(secretRef)
		if err != nil {
			http.Error(w, "secret lookup failed", http.StatusInternalServerError)
			return
		}
		sig := r.Header.Get("X-Wick-Sig")
		if sig == "" || !VerifyHMAC(body, secret, sig) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	matched := h.Router.Dispatch(context.Background(), evt)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"matched":%d}`, matched)
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
