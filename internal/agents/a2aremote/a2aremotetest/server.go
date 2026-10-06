// Package a2aremotetest is a fake remote A2A agent for tests: a real a2asrv
// JSON-RPC handler behind httptest, with a scripted executor and a record
// of what it received.
package a2aremotetest

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// Received is one message the fake got.
type Received struct {
	Text      string
	ContextID string
	TaskID    string
	// Continued is true when the message resumed a stored task.
	Continued bool
}

// Server is the fake. Set the fields before the first request.
type Server struct {
	*httptest.Server
	// Name and Version go on the card.
	Name, Version string
	// Streaming is the card's capabilities.streaming.
	Streaming bool
	// Bearer, when set, is required on every request (card included).
	Bearer string
	// Endpoint overrides the card's interface URL.
	Endpoint string

	mu       sync.Mutex
	received []Received
	auths    []string
}

// New starts a fake named name.
func New(name string) *Server {
	s := &Server{Name: name, Version: "1.0.0", Streaming: true}
	rh := a2asrv.NewHandler(executor{s: s})
	rpc := a2asrv.NewJSONRPCHandler(rh)
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent-card.json", s.serveCard)
	mux.Handle("/rpc", rpc)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		bearer := s.Bearer
		s.mu.Unlock()
		if bearer != "" && r.Header.Get("Authorization") != "Bearer "+bearer {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	return s
}

func (s *Server) serveCard(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	ep := s.Endpoint
	if ep == "" {
		ep = s.URL + "/rpc"
	}
	card := &a2a.AgentCard{
		Name: s.Name, Description: s.Name + " (fake)", Version: s.Version,
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(ep, a2a.TransportProtocolJSONRPC)},
		Capabilities:        a2a.AgentCapabilities{Streaming: s.Streaming},
		DefaultInputModes:   []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"},
		Skills: []a2a.AgentSkill{{ID: "echo", Name: "Echo", Description: "Echoes text", Tags: []string{"echo"}}},
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(card)
}

// Received lists the messages so far.
func (s *Server) Received() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Received(nil), s.received...)
}

// Auths lists the Authorization header of every request so far.
func (s *Server) Auths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auths...)
}

// SetVersion changes the card's version.
func (s *Server) SetVersion(v string) {
	s.mu.Lock()
	s.Version = v
	s.mu.Unlock()
}

// executor scripts replies by the message text:
//
//	"ask …"   → input-required "Which environment?"; the next message on
//	            that task completes with "deploying to <text>"
//	"slow"    → waits 3 s, then completes
//	"big N"   → one artifact of N bytes
//	"fail"    → failed "boom"
//	anything  → "echo: " + text in two artifact chunks, completed
type executor struct{ s *Server }

func (e executor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		text := ""
		for _, p := range ec.Message.Parts {
			text += p.Text()
		}
		e.s.mu.Lock()
		e.s.received = append(e.s.received, Received{
			Text: text, ContextID: ec.ContextID, TaskID: string(ec.TaskID), Continued: ec.StoredTask != nil,
		})
		e.s.mu.Unlock()
		if ec.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		}
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}
		say := func(s string) *a2a.Message {
			return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(s))
		}
		switch {
		case ec.StoredTask != nil && ec.StoredTask.Status.State == a2a.TaskStateInputRequired:
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, say("deploying to "+text)), nil)
		case strings.HasPrefix(text, "ask"):
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateInputRequired, say("Which environment?")), nil)
		case text == "slow":
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				return
			}
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, say("finally")), nil)
		case strings.HasPrefix(text, "big "):
			n := 0
			for _, r := range strings.TrimPrefix(text, "big ") {
				n = n*10 + int(r-'0')
			}
			if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart(strings.Repeat("x", n))), nil) {
				return
			}
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
		case text == "fail":
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateFailed, say("boom")), nil)
		default:
			first := a2a.NewArtifactEvent(ec, a2a.NewTextPart("echo: "))
			if !yield(first, nil) {
				return
			}
			if !yield(a2a.NewArtifactUpdateEvent(ec, first.Artifact.ID, a2a.NewTextPart(text)), nil) {
				return
			}
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
		}
	}
}

func (executor) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}
