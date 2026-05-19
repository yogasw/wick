// Package engine — multi-subscriber event broker.
//
// Engine.OnEvent was a single-slot callback owned by the SSE handler.
// As more consumers want the live stream (workflow_watch long-poll,
// future Loki sink, in-process metrics), the slot becomes a
// bottleneck — only one consumer wins, the rest miss events.
//
// The broker keeps Engine.OnEvent intact for backward compatibility
// (SSE setup still writes to it) and adds an internal subscriber list
// fired in parallel with the legacy hook. Subscribers receive a copy
// of (id, runID, ev) per published event; the broker never blocks on
// a slow subscriber — it skips writes that would block more than a
// few millis so a hung consumer can't stall the engine event loop.
package engine

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// Subscription is the per-call subscription handle returned by
// Engine.Subscribe. Call Cancel() to unregister; safe to call multiple
// times. Receive events via the Ch channel — buffered to avoid losing
// events on routine producers, but bounded so a stalled subscriber
// doesn't pile memory.
type Subscription struct {
	Ch     chan SubEvent
	cancel func()
}

// Cancel unregisters the subscription. Safe to call multiple times.
// After Cancel the broker stops sending to Ch; existing buffered
// events remain readable until the channel is drained, then a
// subsequent receive returns the zero value.
func (s *Subscription) Cancel() {
	if s.cancel != nil {
		s.cancel()
	}
}

// SubEvent is the payload one subscriber receives.
type SubEvent struct {
	WorkflowID string
	RunID      string
	Event      workflow.RunEvent
}

// subscriber is the broker-side bookkeeping for one Subscription.
// done flips to 1 the moment Cancel runs; the publisher uses it as a
// non-blocking guard so we don't keep writing into a channel whose
// consumer has gone away.
type subscriber struct {
	ch   chan SubEvent
	done int32
}

// broker holds the live subscriber list + a mutex. One per Engine.
type broker struct {
	mu   sync.RWMutex
	subs []*subscriber
}

// Subscribe returns a new live subscription. bufferSize is the channel
// buffer (recommend 32; high enough for small bursts, low enough that
// a runaway subscription is loud). Caller MUST defer sub.Cancel() —
// otherwise the subscriber leaks until Engine shutdown.
func (e *Engine) Subscribe(bufferSize int) *Subscription {
	if bufferSize <= 0 {
		bufferSize = 32
	}
	e.ensureBroker()
	s := &subscriber{ch: make(chan SubEvent, bufferSize)}
	e.bus.mu.Lock()
	e.bus.subs = append(e.bus.subs, s)
	e.bus.mu.Unlock()
	return &Subscription{
		Ch: s.ch,
		cancel: func() {
			if !atomic.CompareAndSwapInt32(&s.done, 0, 1) {
				return
			}
			e.bus.mu.Lock()
			for i, x := range e.bus.subs {
				if x == s {
					e.bus.subs = append(e.bus.subs[:i], e.bus.subs[i+1:]...)
					break
				}
			}
			e.bus.mu.Unlock()
			close(s.ch)
		},
	}
}

// ensureBroker lazily attaches a broker to the engine. Called from
// Subscribe so engines that nobody subscribes to don't pay the cost.
func (e *Engine) ensureBroker() {
	e.busInitOnce.Do(func() {
		e.bus = &broker{}
	})
}

// PublishForTest publishes one event to the broker without going
// through the engine's emit pipeline. Test-only — wires into the
// same fan-out code path real events use so subscriber assertions
// stay realistic.
func (e *Engine) PublishForTest(id, runID string, ev workflow.RunEvent) {
	e.ensureBroker()
	e.publishToBus(id, runID, ev)
}

// publishToBus fan-outs one event to every subscriber. Non-blocking:
// a subscriber whose buffer is full gets the event dropped (counted
// via the subscriber's drop counter — currently silent, surface as a
// metric later if it bites). Called from emit AFTER the StateStore
// write succeeds so subscribers see persistent state.
func (e *Engine) publishToBus(id, runID string, ev workflow.RunEvent) {
	if e.bus == nil {
		return
	}
	e.bus.mu.RLock()
	subs := e.bus.subs
	e.bus.mu.RUnlock()
	if len(subs) == 0 {
		return
	}
	se := SubEvent{WorkflowID: id, RunID: runID, Event: ev}
	for _, s := range subs {
		if atomic.LoadInt32(&s.done) != 0 {
			continue
		}
		// Best-effort send. Slow subscriber drops the event rather
		// than back-pressuring the engine. A short timer guards
		// against the rare case where the channel just filled and
		// the consumer is one nanosecond from a receive.
		select {
		case s.ch <- se:
		case <-time.After(5 * time.Millisecond):
			// Drop. Future: counter / log warn.
		}
	}
}
