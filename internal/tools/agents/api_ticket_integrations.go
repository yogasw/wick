package agents

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// secretRedacted is what a stored webhook secret reads as over the API.
//
// A sentinel rather than "" so the editor can tell "this endpoint is signed,
// you just cannot see the key" from "this endpoint is unsigned" — the two
// need different affordances, and showing a blank box for a configured
// secret invites someone to overwrite it by accident.
const secretRedacted = "__stored__"

// ticketDispatcher is the process-wide webhook dispatcher, set at boot. Nil
// means outbound webhooks are not wired (tests, CLI), and every emit is a
// no-op.
var ticketDispatcher *ticket.Dispatcher

// SetTicketDispatcher installs the dispatcher used for test deliveries and
// the delivery log. The same value is registered as the ticket emitter.
func SetTicketDispatcher(d *ticket.Dispatcher) { ticketDispatcher = d }

// newWebhookID mints a stable id for a new endpoint.
func newWebhookID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "wh_fallback"
	}
	return "wh_" + hex.EncodeToString(b)
}

// normaliseWebhooks validates an incoming integrations block and merges in
// what the client could not see.
//
// Three jobs, all of them load-bearing:
//
//   - Mint an id for a new row, so the delivery log survives later edits.
//   - Restore a redacted secret from the stored copy. The client is never
//     given the real value, so an unchanged field comes back as the sentinel
//     and must not be written through as the literal string.
//   - Refuse a URL or event name that could never work, at the only moment
//     it can be reported to a human.
func normaliseWebhooks(next *project.TicketIntegrations, prev project.TicketIntegrations) error {
	if next == nil {
		return nil
	}
	byID := make(map[string]project.TicketWebhook, len(prev.Webhooks))
	for _, w := range prev.Webhooks {
		byID[w.ID] = w
	}

	seen := map[string]bool{}
	out := make([]project.TicketWebhook, 0, len(next.Webhooks))
	for i := range next.Webhooks {
		w := next.Webhooks[i]
		w.URL = strings.TrimSpace(w.URL)
		w.Name = strings.TrimSpace(w.Name)

		// A row with neither a URL nor a name is an empty line in the
		// editor, not a webhook. Dropping it beats rejecting the save.
		if w.URL == "" && w.Name == "" {
			continue
		}
		if w.URL == "" {
			return fmt.Errorf("webhook %q: url is required", w.Name)
		}
		u, err := url.Parse(w.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("webhook %q: url must be a full http(s) URL", w.Name)
		}
		for _, e := range w.Events {
			if !ticket.ValidEvent(e) {
				return fmt.Errorf("webhook %q: unknown event %q", w.Name, e)
			}
		}

		if w.ID == "" {
			w.ID = newWebhookID()
		}
		if seen[w.ID] {
			w.ID = newWebhookID() // duplicated row (copy/paste in the editor)
		}
		seen[w.ID] = true

		// Secret handling: the sentinel means "keep what is stored", and an
		// empty string means the operator deliberately cleared it.
		if w.Secret == secretRedacted {
			w.Secret = byID[w.ID].Secret
		}
		out = append(out, w)
	}
	next.Webhooks = out
	return nil
}

// normaliseTicketButtons validates the custom-button list on its way into
// config. Same shape as the webhook rules: empty editor rows are dropped
// rather than rejected, a URL that could never work is refused at the only
// moment a human can be told, and a new row gets a stable id so the event
// it later fires can name it.
func normaliseTicketButtons(in []project.TicketButton) ([]project.TicketButton, error) {
	seen := map[string]bool{}
	out := make([]project.TicketButton, 0, len(in))
	for _, b := range in {
		b.Label = strings.TrimSpace(b.Label)
		b.URL = strings.TrimSpace(b.URL)
		if b.Label == "" && b.URL == "" {
			continue
		}
		if b.Label == "" {
			return nil, fmt.Errorf("ticket button: label is required")
		}
		if b.URL == "" {
			return nil, fmt.Errorf("ticket button %q: url is required", b.Label)
		}
		u, err := url.Parse(b.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("ticket button %q: url must be a full http(s) URL", b.Label)
		}
		if b.ID == "" {
			b.ID = newTicketButtonID()
		}
		if seen[b.ID] {
			b.ID = newTicketButtonID() // duplicated row (copy/paste in the editor)
		}
		seen[b.ID] = true
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// newTicketButtonID mints a stable id for a new button.
func newTicketButtonID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "btn_fallback"
	}
	return "btn_" + hex.EncodeToString(b)
}

// RedactIntegrations returns a copy safe to send to a client.
//
// Secrets never leave the server: a stored one reads as the sentinel so the
// UI can show "signed" without ever holding the key. Callers rendering the
// settings page must go through this.
func RedactIntegrations(in project.TicketIntegrations) project.TicketIntegrations {
	out := in
	out.Webhooks = make([]project.TicketWebhook, len(in.Webhooks))
	for i, w := range in.Webhooks {
		if w.Secret != "" {
			w.Secret = secretRedacted
		}
		out.Webhooks[i] = w
	}
	return out
}

// redactTicketConfig strips webhook secrets from a config on its way to a
// client. Every read path that serves a TicketConfig goes through here.
func redactTicketConfig(in project.TicketConfig) project.TicketConfig {
	in.Integrations = RedactIntegrations(in.Integrations)
	return in
}

// apiTicketEvents handles GET /api/ticket-events — the event catalogue, so
// the settings UI and the docs never drift from the code.
func apiTicketEvents(c *tool.Ctx) {
	c.JSON(http.StatusOK, map[string]any{"events": ticket.AllEvents()})
}

// apiTicketWebhookDeliveries handles
// GET /api/projects/{id}/ticket-webhooks/{webhookID}/deliveries.
//
// The delivery log is why a broken integration is debuggable from the
// settings page instead of by grepping the server log.
func apiTicketWebhookDeliveries(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	projectID := c.PathValue("id")
	if _, ok := globalMgr.Registry().Project(projectID); !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if ticketDispatcher == nil {
		c.JSON(http.StatusOK, map[string]any{"deliveries": []any{}})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"deliveries": ticketDispatcher.Deliveries(c.PathValue("webhookID")),
	})
}

// apiTicketWebhookTest handles
// POST /api/projects/{id}/ticket-webhooks/{webhookID}/test.
//
// Sends a synthetic event to one configured endpoint and reports the
// outcome, so an operator can prove the wiring works before a real ticket
// depends on it. Admin-only: it makes the server issue an outbound request
// to an operator-supplied URL.
func apiTicketWebhookTest(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	u := login.GetUser(c.Context())
	if u == nil || !u.IsAdmin() {
		c.JSON(http.StatusForbidden, map[string]string{"error": "admin only"})
		return
	}
	projectID := c.PathValue("id")
	p, ok := globalMgr.Registry().Project(projectID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if ticketDispatcher == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "webhook dispatcher not wired"})
		return
	}
	webhookID := c.PathValue("webhookID")
	for _, w := range p.Meta.Ticket.Integrations.Webhooks {
		if w.ID == webhookID {
			// Test the endpoint as configured, but ignore the enabled flag:
			// the point is to check a row before switching it on.
			w.Enabled = true
			c.JSON(http.StatusOK, ticketDispatcher.SendTest(w, projectID))
			return
		}
	}
	c.JSON(http.StatusNotFound, map[string]string{"error": "webhook not found"})
}
