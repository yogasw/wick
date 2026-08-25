package ticket

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/project"
)

// Delivery headers. A receiver identifies the event from the header without
// parsing the body, and dedupes on the delivery id.
const (
	HeaderEvent     = "X-Wick-Event"
	HeaderDelivery  = "X-Wick-Delivery"
	HeaderSignature = "X-Wick-Signature"
)

// Retry schedule. Three attempts over ~30s: enough to ride out a receiver
// restart or a brief network blip, short enough that a permanently dead
// endpoint stops consuming a goroutine quickly. Deliveries are fire and
// forget, so there is no durable queue behind this — a receiver that must
// never miss an event should reconcile via the REST API on startup, which
// the docs say plainly.
var retryBackoff = []time.Duration{time.Second, 5 * time.Second, 25 * time.Second}

// attemptTimeout caps one HTTP attempt.
const attemptTimeout = 10 * time.Second

// maxLogPerWebhook is how many deliveries are remembered per endpoint. The
// log exists so a broken integration is debuggable from the settings page
// instead of the server log; 20 is enough to see a pattern and small enough
// to keep in memory forever.
const maxLogPerWebhook = 20

// ConfigLookup returns the ticket config for a project. Injected so the
// dispatcher can read a project's webhook list without the ticket package
// importing the project registry.
type ConfigLookup func(projectID string) (project.TicketConfig, bool)

// Delivery is one recorded attempt, for the settings UI.
type Delivery struct {
	WebhookID string    `json:"webhook_id"`
	Event     string    `json:"event"`
	At        time.Time `json:"at"`
	Status    int       `json:"status,omitempty"`
	Err       string    `json:"error,omitempty"`
	Attempts  int       `json:"attempts"`
	OK        bool      `json:"ok"`
}

// Dispatcher delivers ticket events to a project's configured webhooks.
//
// It implements Emitter, so installing it is the single wiring point that
// turns ticket mutations into outbound HTTP.
type Dispatcher struct {
	Lookup ConfigLookup
	// Client is the HTTP client used for deliveries. Left nil, a client
	// with attemptTimeout is used.
	Client *http.Client
	// AllowPrivate lifts the private-address guard. Off by default: a
	// webhook URL is user-supplied and the server fetches it, so the
	// default must not let a tenant point wick at its own metadata
	// service. Operators running an all-private deployment set this.
	AllowPrivate bool

	mu  sync.Mutex
	log map[string][]Delivery
}

// NewDispatcher builds a dispatcher over a config lookup.
func NewDispatcher(lookup ConfigLookup) *Dispatcher {
	return &Dispatcher{
		Lookup: lookup,
		Client: &http.Client{Timeout: attemptTimeout},
		log:    map[string][]Delivery{},
	}
}

// Emit fans the event out to every webhook that wants it.
//
// Each delivery runs in its own goroutine: a ticket write must never wait on
// somebody's slow endpoint, and one dead receiver must not delay the others.
func (d *Dispatcher) Emit(ev Event) {
	if d == nil || d.Lookup == nil {
		return
	}
	cfg, ok := d.Lookup(ev.ProjectID)
	if !ok {
		return
	}
	for _, w := range cfg.ActiveWebhooks(ev.Event) {
		go d.deliver(w, ev)
	}
}

// Deliveries returns the recorded attempts for a webhook, newest first.
func (d *Dispatcher) Deliveries(webhookID string) []Delivery {
	d.mu.Lock()
	defer d.mu.Unlock()
	src := d.log[webhookID]
	out := make([]Delivery, len(src))
	for i, v := range src {
		out[len(src)-1-i] = v
	}
	return out
}

// SendTest delivers a synthetic event to one webhook and reports the
// outcome, so the settings page can prove an endpoint works before a real
// ticket depends on it. Synchronous by design: the operator clicked a button
// and is waiting for the answer.
func (d *Dispatcher) SendTest(w project.TicketWebhook, projectID string) Delivery {
	ev := Event{
		Event:       EventUpdated,
		ProjectID:   projectID,
		DeliveredAt: time.Now().UTC(),
		Actor:       Actor{Type: ActorSystem, Name: "webhook test"},
		Ticket: Ticket{
			ID:        "T-TEST",
			ProjectID: projectID,
			Title:     "Test event from wick",
			Status:    "open",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
		Changes: map[string]Change{"status": {From: "open", To: "open"}},
	}
	if id, err := newID(); err == nil {
		ev.ID = "evt_" + strings.TrimPrefix(id, "T-")
	}
	return d.deliver(w, ev)
}

// deliver POSTs the event, retrying on failure, and records the outcome.
func (d *Dispatcher) deliver(w project.TicketWebhook, ev Event) Delivery {
	rec := Delivery{WebhookID: w.ID, Event: ev.Event, At: time.Now().UTC()}

	body, err := json.Marshal(ev)
	if err != nil {
		rec.Err = "encode event: " + err.Error()
		d.record(rec)
		return rec
	}
	if err := d.checkURL(w.URL); err != nil {
		rec.Err = err.Error()
		d.record(rec)
		return rec
	}

	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: attemptTimeout}
	}

	for attempt := 0; attempt < len(retryBackoff); attempt++ {
		if attempt > 0 {
			time.Sleep(retryBackoff[attempt-1])
		}
		rec.Attempts = attempt + 1
		status, derr := d.attempt(client, w, ev, body)
		rec.Status, rec.Err = status, ""
		if derr != nil {
			rec.Err = derr.Error()
		}
		// 2xx is success. A 4xx other than 408/429 is the receiver saying
		// the request itself is wrong, so retrying it would just repeat the
		// same rejection — stop and keep the status for the operator.
		if derr == nil && status >= 200 && status < 300 {
			rec.OK = true
			break
		}
		if status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests {
			break
		}
	}

	if !rec.OK {
		l := log.With().Str("component", "ticket-webhook").Logger()
		l.Warn().
			Str("webhook", w.ID).
			Str("event", ev.Event).
			Int("status", rec.Status).
			Int("attempts", rec.Attempts).
			Str("error", rec.Err).
			Msg("webhook delivery failed")
	}
	d.record(rec)
	return rec
}

// attempt makes one HTTP POST.
func (d *Dispatcher) attempt(client *http.Client, w project.TicketWebhook, ev Event, body []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), attemptTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "wick-tickets/1")
	req.Header.Set(HeaderEvent, ev.Event)
	req.Header.Set(HeaderDelivery, ev.ID)
	if w.Secret != "" {
		req.Header.Set(HeaderSignature, Sign(w.Secret, body))
	}
	// Custom headers last so a receiver can override the defaults, but
	// never the signature — that would let a misconfiguration silently
	// disable verification.
	for k, v := range w.Headers {
		if strings.EqualFold(k, HeaderSignature) {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// Sign returns the X-Wick-Signature value for a raw body.
//
// The signature covers the bytes on the wire, not a re-serialised copy: JSON
// key order and whitespace are not stable across languages, so a receiver
// that re-encodes before verifying will fail. The docs say this too.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether sig matches body under secret. Exported so the
// docs can point at a real implementation, and used by tests.
func Verify(secret string, body []byte, sig string) bool {
	return hmac.Equal([]byte(Sign(secret, body)), []byte(sig))
}

// checkURL rejects a destination before anything is sent.
//
// A webhook URL is user-supplied and fetched by the server, which makes it
// an SSRF vector: without this, a tenant could point a webhook at the cloud
// metadata endpoint or an internal admin port and use ticket events as the
// trigger. Hostnames are resolved and every returned address checked,
// because a public name can resolve to a private address.
func (d *Dispatcher) checkURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid webhook url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook url must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("webhook url has no host")
	}
	if d.AllowPrivate {
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("webhook url resolves to a private address (%s) — refused", ip)
		}
	}
	return nil
}

// isPrivateIP reports whether ip is one a webhook must not reach.
func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	// Carrier-grade NAT (100.64.0.0/10) is not covered by IsPrivate but is
	// where a cloud metadata service often sits.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}
	return false
}

// record appends to the per-webhook ring buffer.
func (d *Dispatcher) record(rec Delivery) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.log == nil {
		d.log = map[string][]Delivery{}
	}
	list := append(d.log[rec.WebhookID], rec)
	if len(list) > maxLogPerWebhook {
		list = list[len(list)-maxLogPerWebhook:]
	}
	d.log[rec.WebhookID] = list
}
