package a2aremote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
)

// cardMaxBytes caps an agent card fetch; a card is a few KB.
const cardMaxBytes = 1 << 20

// resolveTimeout bounds a card fetch: a person waits on it in a form.
const resolveTimeout = 15 * time.Second

// Resolved is a fetched agent card.
type Resolved struct {
	CardURL string
	Card    *a2a.AgentCard
	JSON    json.RawMessage
}

// SplitCardURL turns what a person pasted into the resolver's base URL and
// card path: a URL ending in ".json" (or under /.well-known/) is the card
// itself, anything else is the agent's base URL.
func SplitCardURL(u *url.URL) (base, path string) {
	p := strings.TrimRight(u.EscapedPath(), "/")
	if strings.HasSuffix(strings.ToLower(p), ".json") || strings.Contains(p, "/.well-known/") {
		return u.Scheme + "://" + u.Host, p
	}
	return u.Scheme + "://" + u.Host + p, "/.well-known/agent-card.json"
}

// Resolve fetches and checks the agent card at raw. Every interface the
// card names must pass the guard too, so a public card cannot point turns
// at an internal address.
func Resolve(ctx context.Context, g Guard, raw string, auth PlainAuth) (Resolved, error) {
	u, err := g.CheckURL(raw)
	if err != nil {
		return Resolved{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	base, path := SplitCardURL(u)
	var body []byte
	parse := func(b []byte) (*a2a.AgentCard, error) {
		body = append([]byte(nil), b...)
		return agentcard.DefaultCardParser(b)
	}
	r := agentcard.Resolver{Client: g.HTTPClient(auth, cardMaxBytes), CardParser: parse}
	card, err := r.Resolve(ctx, base, agentcard.WithPath(path))
	if err != nil {
		return Resolved{}, fmt.Errorf("fetch agent card: %w", err)
	}
	if strings.TrimSpace(card.Name) == "" {
		return Resolved{}, errors.New("agent card has no name")
	}
	if len(card.SupportedInterfaces) == 0 {
		return Resolved{}, errors.New("agent card names no endpoint (supportedInterfaces is empty)")
	}
	for _, in := range card.SupportedInterfaces {
		if in == nil {
			continue
		}
		if _, err := g.CheckURL(in.URL); err != nil {
			return Resolved{}, fmt.Errorf("agent card endpoint %s: %w", in.URL, err)
		}
	}
	return Resolved{CardURL: base + path, Card: card, JSON: body}, nil
}

// NewClient builds an A2A client for card over the guarded HTTP client.
func NewClient(ctx context.Context, g Guard, card *a2a.AgentCard, auth PlainAuth, maxBody int64) (*a2aclient.Client, error) {
	for _, in := range card.SupportedInterfaces {
		if in == nil {
			continue
		}
		if _, err := g.CheckURL(in.URL); err != nil {
			return nil, fmt.Errorf("agent card endpoint %s: %w", in.URL, err)
		}
	}
	// The body cap is per HTTP response and counts the JSON envelope, so
	// it is looser than the content cap the runtime enforces.
	hc := g.HTTPClient(auth, 2*maxBody+64<<10)
	return a2aclient.NewFromCard(ctx, card, a2aclient.WithJSONRPCTransport(hc), a2aclient.WithRESTTransport(hc))
}

// PingResult is the Test button's answer.
type PingResult struct {
	OK        bool   `json:"ok"`
	State     string `json:"state"`
	LatencyMS int64  `json:"latency_ms"`
	Reply     string `json:"reply,omitempty"`
	Error     string `json:"error,omitempty"`
}

// pingTimeout bounds the Test button.
const pingTimeout = 30 * time.Second

// Ping sends "ping" with message/send and reports the reply and latency.
func Ping(ctx context.Context, g Guard, card *a2a.AgentCard, auth PlainAuth) PingResult {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	client, err := NewClient(ctx, g, card, auth, DefaultMaxResponseBytes)
	if err != nil {
		return PingResult{State: "client_failed", Error: err.Error()}
	}
	defer func() { _ = client.Destroy() }()
	start := time.Now()
	out, err := client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	})
	res := PingResult{LatencyMS: time.Since(start).Milliseconds()}
	if err != nil {
		res.State, res.Error = "send_failed", err.Error()
		return res
	}
	switch v := out.(type) {
	case *a2a.Message:
		res.State, res.OK, res.Reply = "message", true, MessageText(v)
	case *a2a.Task:
		res.State = string(v.Status.State)
		res.Reply = MessageText(v.Status.Message)
		if res.Reply == "" {
			for _, a := range v.Artifacts {
				res.Reply += partsText(a.Parts)
			}
		}
		res.OK = v.Status.State != a2a.TaskStateFailed && v.Status.State != a2a.TaskStateRejected
	default:
		res.State, res.Error = "send_failed", fmt.Sprintf("unexpected result %T", out)
	}
	res.Reply = truncate(res.Reply, 2000)
	return res
}

// MessageText is m's parts as text.
func MessageText(m *a2a.Message) string {
	if m == nil {
		return ""
	}
	return partsText(m.Parts)
}

// partsText renders parts as text: text as is, a file as a link line, data
// as a JSON block.
func partsText(parts a2a.ContentParts) string {
	var sb strings.Builder
	for _, p := range parts {
		if p == nil {
			continue
		}
		switch v := p.Content.(type) {
		case a2a.Text:
			sb.WriteString(string(v))
		case a2a.URL:
			name := p.Filename
			if name == "" {
				name = string(v)
			}
			fmt.Fprintf(&sb, "\n📎 [%s](%s)\n", name, string(v))
		case a2a.Raw:
			name := p.Filename
			if name == "" {
				name = "file"
			}
			fmt.Fprintf(&sb, "\n📎 %s (%s, %d bytes, inline)\n", name, p.MediaType, len(v))
		case a2a.Data:
			b, err := json.MarshalIndent(v.Value, "", "  ")
			if err == nil {
				sb.WriteString("\n```json\n" + string(b) + "\n```\n")
			}
		}
	}
	return sb.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
