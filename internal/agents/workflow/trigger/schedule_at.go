package trigger

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// ScheduleAtScheduler fires one-shot triggers whose type is schedule_at.
// Each trigger has an At time.Time field; the scheduler arms a timer for
// every trigger that hasn't fired yet (At is in the future at Sync time).
//
// Unlike CronScheduler, timers are one-shot: after firing they are removed.
// If DeleteAfter is set on the trigger, the scheduler also calls the
// optional RemoveFn so the workflow service can drop the trigger from YAML.
//
// Wired alongside CronScheduler in Manager.Start / HotReload / Bootstrap.
type ScheduleAtScheduler struct {
	router *Router
	mu     sync.Mutex
	timers map[timerKey]*time.Timer // (id, triggerID) → pending timer
	// RemoveFn is called (id, triggerID) when a delete_after trigger fires.
	// Optional — pass nil to skip the post-fire cleanup.
	RemoveFn func(id, triggerID string)
}

type timerKey struct {
	id        string
	triggerID string
}

// NewScheduleAtScheduler wires a scheduler against a Router.
func NewScheduleAtScheduler(r *Router) *ScheduleAtScheduler {
	return &ScheduleAtScheduler{
		router: r,
		timers: map[timerKey]*time.Timer{},
	}
}

// Sync reconciles all schedule_at triggers for one workflow id.
// Call on Bootstrap and HotReload. Cancels stale timers for triggers
// that were removed or already have an At in the past.
func (s *ScheduleAtScheduler) Sync(id string, w workflow.Workflow) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Build the new set of desired timers.
	wanted := map[string]workflow.Trigger{}
	for _, tr := range w.Triggers {
		if tr.Type != workflow.TriggerScheduleAt {
			continue
		}
		if tr.At.IsZero() || !tr.At.After(time.Now()) {
			// Already past — don't arm.
			continue
		}
		tid := tr.ID
		if tid == "" {
			tid = string(tr.Type) // fallback for hand-edited YAML
		}
		wanted[tid] = tr
	}

	// Cancel timers for triggers that are no longer present.
	for k, t := range s.timers {
		if k.id != id {
			continue
		}
		if _, ok := wanted[k.triggerID]; !ok {
			t.Stop()
			delete(s.timers, k)
		}
	}

	// Arm new timers (skip if already armed for same (id, triggerID)).
	for tid, tr := range wanted {
		k := timerKey{id: id, triggerID: tid}
		if _, exists := s.timers[k]; exists {
			continue
		}
		delay := time.Until(tr.At)
		entry := tr // capture
		entryID := tid
		t := time.AfterFunc(delay, func() {
			s.fire(id, entryID, entry)
		})
		s.timers[k] = t
		log.Info().Str("wf_id", id).Str("trigger", entryID).
			Str("at", tr.At.UTC().Format(time.RFC3339)).
			Msg("workflow schedule_at: armed")
	}
}

// Unsync cancels all pending timers for id (called on workflow delete).
func (s *ScheduleAtScheduler) Unsync(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, t := range s.timers {
		if k.id == id {
			t.Stop()
			delete(s.timers, k)
		}
	}
}

func (s *ScheduleAtScheduler) fire(id, triggerID string, tr workflow.Trigger) {
	// Remove from map — one-shot.
	s.mu.Lock()
	k := timerKey{id: id, triggerID: triggerID}
	delete(s.timers, k)
	s.mu.Unlock()

	evt := workflow.Event{
		Type: string(workflow.TriggerScheduleAt),
		At:   tr.At.UTC(),
	}
	if tr.EntryNode != "" {
		evt.Payload = map[string]any{"entry_node": tr.EntryNode}
	}
	if err := s.router.RunNow(context.Background(), id, evt); err != nil {
		log.Warn().Str("wf_id", id).Str("trigger", triggerID).Err(err).
			Msg("workflow schedule_at: enqueue failed")
		return
	}
	log.Info().Str("wf_id", id).Str("trigger", triggerID).
		Str("at", tr.At.UTC().Format(time.RFC3339)).
		Msg("workflow schedule_at fired")

	if tr.DeleteAfter && s.RemoveFn != nil {
		s.RemoveFn(id, triggerID)
	}
}
