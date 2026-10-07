package a2aremote

import (
	"context"
	"errors"
	"time"

	"github.com/yogasw/wick/internal/agents/remote"
)

// AdapterKind is the remote.Source kind of an A2A remote agent.
const AdapterKind = "a2a"

func init() {
	remote.Register(remote.Adapter{
		Kind: AdapterKind, Label: "A2A", Schema: remote.SchemaVersion,
		// Stream first; a task that is still working after the stream (or
		// a non-streaming send) is polled with tasks/get inside the call.
		Listen: []remote.ListenMode{remote.ListenPush},
	})
}

// Source is the A2A adapter: each turn is one call to the remote, whose
// events are pushed as they stream.
type Source struct {
	rt      Runtime
	pending chan remote.Event
}

// NewSource wraps rt.
func NewSource(rt Runtime) *Source { return &Source{rt: rt} }

func (s *Source) Kind() string                { return AdapterKind }
func (s *Source) Label() string               { return "a2a-remote (" + hostOf(s.rt.Config.Card.Endpoint) + ")" }
func (s *Source) Listen() []remote.ListenMode { return []remote.ListenMode{remote.ListenPush} }

// Limits: the call is bounded by the agent's timeout; A2A says when it is
// done, so there is no idle end.
func (s *Source) Limits() remote.Limits { return remote.Limits{Max: s.rt.Config.Timeout()} }

// ResumeID is the session's contextId, when it has one.
func (s *Source) ResumeID(dir string) string { return LoadState(dir).ContextID }

// Send starts the call; its events are read with Receive. Turns of one
// session run one at a time, so one pending channel is enough.
func (s *Source) Send(ctx context.Context, turn remote.Turn) (remote.Handle, error) {
	ch := make(chan remote.Event, 64)
	s.pending = ch
	go s.run(ctx, turn, ch)
	return remote.Handle{ID: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

// Receive returns the events of the call Send started.
func (s *Source) Receive(context.Context, remote.Handle) (<-chan remote.Event, error) {
	if s.pending == nil {
		return nil, errors.New("a2aremote: no call in flight")
	}
	return s.pending, nil
}

// Cancel: the call stops with the turn's context.
func (s *Source) Cancel(context.Context, remote.Handle) error { return nil }

func (s *Source) End(remote.Handle) { s.pending = nil }

// Describe names the remote from its card.
func (s *Source) Describe(context.Context) (remote.Description, error) {
	c := s.rt.Config.Card
	return remote.Description{Kind: AdapterKind, Name: c.Name, Detail: c.Description, Target: c.Endpoint}, nil
}

// Test pings the remote with its card.
func (s *Source) Test(ctx context.Context) remote.TestResult {
	card, err := s.rt.Config.ParsedCard()
	if err != nil {
		return remote.TestResult{State: "card_failed", Error: err.Error()}
	}
	r := Ping(ctx, s.rt.Guard, card, s.rt.Auth)
	return remote.TestResult{OK: r.OK, State: r.State, LatencyMS: r.LatencyMS, Reply: r.Reply, Error: r.Error}
}
