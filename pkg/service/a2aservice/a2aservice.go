// Package a2aservice turns a plain reply function into an A2A agent served
// by a service plugin: the agent card at /.well-known/agent.json (and
// agent-card.json), JSON-RPC message/send and message/stream (SSE) at "/".
// It is the core of an "A2A repeater": put any bot API behind Reply and the
// bot becomes an A2A agent wick (or anyone) can add as a remote agent.
package a2aservice

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/yogasw/wick/pkg/service"
)

// Reply answers one message of conversation contextID. Each value sent on
// chunks is streamed to message/stream callers as it comes; the returned
// error fails the task.
type Reply func(ctx context.Context, contextID, text string, chunks chan<- string) error

// Card is what the agent card says about the agent.
type Card struct {
	Name, Description, Version string
}

// Mount serves the agent on mux (paths relative to the plugin root).
func Mount(mux *http.ServeMux, card Card, reply Reply) {
	rpc := a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor{reply: reply}))
	serveCard := func(w http.ResponseWriter, r *http.Request) {
		v := card.Version
		if v == "" {
			v = "1.0.0"
		}
		c := &a2a.AgentCard{
			Name: card.Name, Description: card.Description, Version: v,
			SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(service.BaseURL(r)+"/", a2a.TransportProtocolJSONRPC)},
			Capabilities:        a2a.AgentCapabilities{Streaming: true},
			DefaultInputModes:   []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"},
			Skills: []a2a.AgentSkill{{ID: "chat", Name: "Chat", Description: card.Description, Tags: []string{"chat"}}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(c)
	}
	mux.HandleFunc("GET /.well-known/agent.json", serveCard)
	mux.HandleFunc("GET /.well-known/agent-card.json", serveCard)
	mux.Handle("POST /{$}", rpc)
}

type executor struct{ reply Reply }

func (e executor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		text := ""
		for _, p := range ec.Message.Parts {
			text += p.Text()
		}
		if ec.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		}
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}
		chunks := make(chan string)
		errc := make(chan error, 1)
		go func() {
			defer close(chunks)
			errc <- e.reply(ctx, ec.ContextID, text, chunks)
		}()
		var artID a2a.ArtifactID
		for c := range chunks {
			var ev a2a.Event
			if artID == "" {
				first := a2a.NewArtifactEvent(ec, a2a.NewTextPart(c))
				artID = first.Artifact.ID
				ev = first
			} else {
				ev = a2a.NewArtifactUpdateEvent(ec, artID, a2a.NewTextPart(c))
			}
			if !yield(ev, nil) {
				return
			}
		}
		if err := <-errc; err != nil {
			msg := a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(err.Error()))
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateFailed, msg), nil)
			return
		}
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
	}
}

func (executor) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}
