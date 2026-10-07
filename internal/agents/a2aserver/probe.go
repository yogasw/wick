package a2aserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
)

// ProbeResult is the connection test's answer.
type ProbeResult struct {
	OK     bool   `json:"ok"`
	Status string `json:"status"`
	// CardMS and SendMS time the agent card fetch and the ping round trip.
	CardMS int64  `json:"card_ms"`
	SendMS int64  `json:"send_ms"`
	Reply  string `json:"reply,omitempty"`
	Error  string `json:"error,omitempty"`
}

// inProcess serves requests straight into the server's own routes: the
// test exercises the real HTTP surface (routing, auth, JSON-RPC, executor)
// without a socket, so nothing leaves the host — not even to the public
// URL the card names.
type inProcess struct{ h http.Handler }

func (t inProcess) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, r.WithContext(withProbe(r.Context())))
	return rec.Result(), nil
}

// Probe fetches agentID's card and sends it a "ping" through an A2A
// client, the way a remote caller would.
func (s *Server) Probe(ctx context.Context, agentID string) ProbeResult {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	hc := &http.Client{Transport: inProcess{s.mux}}
	base := s.baseURL()
	if base == "" {
		base = "http://wick.local"
	}

	start := time.Now()
	resolver := agentcard.Resolver{Client: hc, CardParser: agentcard.DefaultCardParser}
	card, err := resolver.Resolve(ctx, base, agentcard.WithPath(CardPath(agentID)))
	res := ProbeResult{CardMS: time.Since(start).Milliseconds()}
	if err != nil {
		res.Status, res.Error = "card_failed", err.Error()
		return res
	}

	client, err := a2aclient.NewFromCard(ctx, card, a2aclient.WithJSONRPCTransport(hc))
	if err != nil {
		res.Status, res.Error = "client_failed", err.Error()
		return res
	}
	defer func() { _ = client.Destroy() }()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping"))
	start = time.Now()
	out, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg, Metadata: map[string]any{HealthKey: true}})
	res.SendMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Status, res.Error = "send_failed", err.Error()
		return res
	}
	task, ok := out.(*a2a.Task)
	if !ok {
		res.Status, res.Error = "send_failed", fmt.Sprintf("unexpected result %T", out)
		return res
	}
	if task.Status.Message != nil {
		res.Reply = messageText(task.Status.Message)
	}
	res.Status = string(task.Status.State)
	res.OK = task.Status.State == a2a.TaskStateCompleted
	return res
}
