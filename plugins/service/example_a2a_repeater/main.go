// Command example_a2a_repeater is an A2A repeater: it puts a client's bot
// API behind the A2A protocol so wick (or any A2A client) can talk to it as
// a remote agent, and it also offers the same bot as a Team remote_source.
//
// The "client bot" here is simulated in-process (botAPI): it opens a
// conversation, then answers each message with an echo. A real repeater
// swaps botAPI for HTTP calls to the client's API, with credentials in
// Configs (stored encrypted by wick).
//
// Surfaces under /x/example_a2a_repeater:
//
//	GET  /.well-known/agent.json  agent card (public)
//	POST /                        JSON-RPC message/send, message/stream (SSE)
//
// A2A contextId ↔ bot conversation id is kept in memory.
package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/service"
	"github.com/yogasw/wick/pkg/service/a2aservice"
)

func main() { service.ServeService(module()) }

// botAPI simulates the client's bot API: conversations and replies.
type botAPI struct {
	next  atomic.Int64
	mu    sync.Mutex
	turns map[string]int
}

func newBot() *botAPI { return &botAPI{turns: map[string]int{}} }

// open starts a bot conversation.
func (b *botAPI) open() string { return fmt.Sprintf("conv-%d", b.next.Add(1)) }

// reply answers text in conv, as words (a bot API that streams).
func (b *botAPI) reply(conv, prefix, text string) []string {
	b.mu.Lock()
	b.turns[conv]++
	n := b.turns[conv]
	b.mu.Unlock()
	out := fmt.Sprintf("%s%s (turn %d of %s)", prefix, text, n, conv)
	return strings.SplitAfter(out, " ")
}

// repeater maps A2A contextIds (and remote_source sessions) to bot
// conversations.
type repeater struct {
	bot    *botAPI
	prefix func() string

	mu    sync.Mutex
	convs map[string]string // contextId → bot conversation
}

func (r *repeater) conv(contextID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.convs[contextID]
	if !ok {
		c = r.bot.open()
		r.convs[contextID] = c
	}
	return c
}

// a2aReply is the a2aservice.Reply of the repeater.
func (r *repeater) a2aReply(_ context.Context, contextID, text string, chunks chan<- string) error {
	for _, w := range r.bot.reply(r.conv(contextID), r.prefix(), text) {
		chunks <- w
	}
	return nil
}

func module() service.Module {
	var env *service.Env
	rep := &repeater{bot: newBot(), convs: map[string]string{}, prefix: func() string {
		if env != nil && env.Cfg("prefix") != "" {
			return env.Cfg("prefix")
		}
		return "echo: "
	}}
	return service.Module{
		Meta: service.Meta{Key: "example_a2a_repeater", Name: "A2A repeater (echo bot)",
			Description: "Puts a bot API behind A2A; this example's bot echoes.", Icon: "🔁"},
		Routes:  []service.Route{{Prefix: "/", Auth: service.Public}},
		Configs: []entity.Config{{Key: "prefix", Value: "echo: ", Description: "Text the echo bot puts before each reply"}},
		Register: func(mux *http.ServeMux, e *service.Env) {
			env = e
			a2aservice.Mount(mux, a2aservice.Card{Name: "Echo bot", Description: "A client bot reached through the A2A repeater."}, rep.a2aReply)
		},
		RemoteSource: &remoteSource{rep: rep, turns: map[string]chan service.RemoteEvent{}},
	}
}

// remoteSource offers the same bot as a Team remote agent source.
type remoteSource struct {
	rep  *repeater
	next atomic.Int64

	mu    sync.Mutex
	turns map[string]chan service.RemoteEvent
}

func (s *remoteSource) Send(_ context.Context, t service.RemoteTurn) (service.RemoteSendResult, error) {
	ctxID := t.ContextID
	if ctxID == "" {
		ctxID = fmt.Sprintf("wick-%d", s.next.Add(1))
	}
	words := s.rep.bot.reply(s.rep.conv(ctxID), s.rep.prefix(), t.Text)
	ch := make(chan service.RemoteEvent, len(words)+1)
	for _, w := range words {
		ch <- service.RemoteEvent{Kind: "text_delta", Text: w}
	}
	ch <- service.RemoteEvent{Kind: "done"}
	close(ch)
	h := fmt.Sprintf("turn-%d", s.next.Add(1))
	s.mu.Lock()
	s.turns[h] = ch
	s.mu.Unlock()
	return service.RemoteSendResult{Handle: h, ContextID: ctxID}, nil
}

func (s *remoteSource) Receive(_ context.Context, h string) (<-chan service.RemoteEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.turns[h]
	if !ok {
		return nil, fmt.Errorf("unknown turn %q", h)
	}
	return ch, nil
}

func (s *remoteSource) Done(h string) {
	s.mu.Lock()
	delete(s.turns, h)
	s.mu.Unlock()
}

func (s *remoteSource) Describe() (string, string) {
	return "Echo bot", "Client bot behind the A2A repeater"
}
