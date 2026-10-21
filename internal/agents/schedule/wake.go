package schedule

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// A watch with WakeOn is woken by events instead of (or on top of) its
// timer. A plugin publishes "<its key>:<topic>" through the Host API
// (events.publish); every live watch whose WakeOn matches runs right away,
// in this process's runner, with the event payload as its first step's
// input. Nothing about the run changes otherwise: it runs as the watch's
// owner, through the same steps, history and single notice, so a webhook
// can end a watch the moment the thing it waits for happens without the
// plugin holding anybody's credentials or choosing which session hears it.
//
// A WakeOn watch without every/cron is wake-only: its timer slot is its
// time limit (or never, with timeout off), so it costs nothing while it
// waits. With every/cron the timer keeps polling as before and events only
// add runs in between.
//
// Queued events live in memory: a restart drops the ones not yet run, and
// only the process holding the watch lease (the one running the watches)
// can be woken.

const (
	// wakeQueueMax bounds the events queued for one watch while it runs;
	// the oldest is dropped past it.
	wakeQueueMax = 20
	// wakeTopicMax bounds a WakeOn pattern and a published topic.
	wakeTopicMax = 255
)

// wakeNever is the timer slot of a wake-only watch without a time limit.
var wakeNever = time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)

var wakeKeyRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9_-]{0,62}[a-z0-9])?$`)

// ErrNoWakeRunner is returned by PublishEvent when this process runs no
// schedule runner.
var ErrNoWakeRunner = errors.New("no schedule runner in this process")

type wakeEvent struct {
	topic   string
	payload []byte
}

// NormalizeWakeOn validates a WakeOn pattern: "<plugin key>:<topic>", the
// topic non-empty without spaces, an optional trailing * for a prefix.
// Empty stays empty (no subscription).
func NormalizeWakeOn(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", nil
	}
	key, topic, ok := strings.Cut(p, ":")
	if !ok || topic == "" {
		return "", fmt.Errorf(`wake_on: %q must be "<plugin key>:<topic>", e.g. "gitlab_webhook:group/repo"`, raw)
	}
	if !wakeKeyRE.MatchString(key) {
		return "", fmt.Errorf("wake_on: %q is not a plugin key", key)
	}
	if err := checkTopic(topic); err != nil {
		return "", fmt.Errorf("wake_on: %w", err)
	}
	if i := strings.Index(topic, "*"); i >= 0 && i != len(topic)-1 {
		return "", errors.New("wake_on: * is only allowed at the end (prefix match)")
	}
	if len(p) > wakeTopicMax {
		return "", fmt.Errorf("wake_on: longer than %d characters", wakeTopicMax)
	}
	return p, nil
}

func checkTopic(topic string) error {
	if topic == "" {
		return errors.New("topic is empty")
	}
	if len(topic) > wakeTopicMax {
		return fmt.Errorf("topic longer than %d characters", wakeTopicMax)
	}
	for _, r := range topic {
		if r <= ' ' || r == 0x7f {
			return errors.New("topic must not contain spaces or control characters")
		}
	}
	return nil
}

// WakeMatches reports whether topic satisfies pattern, case-insensitively;
// a pattern ending in * matches any topic with that prefix.
func WakeMatches(pattern, topic string) bool {
	pattern, topic = strings.ToLower(pattern), strings.ToLower(topic)
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(topic, prefix)
	}
	return pattern == topic
}

// IsWakeOnly reports whether m waits for events only (WakeOn, no cadence).
func IsWakeOnly(m entity.ScheduledMessage) bool {
	return m.WakeOn != "" && m.IntervalMs <= 0 && strings.TrimSpace(m.Cron) == ""
}

// wakeDeadline is the timer slot of a wake-only watch: its time limit, or
// never.
func wakeDeadline(m entity.ScheduledMessage) time.Time {
	if m.EndsAt != nil {
		return *m.EndsAt
	}
	return wakeNever
}

// WakeOnlyFirstRun is the stored run_at of a new wake-only watch.
func WakeOnlyFirstRun(endsAt *time.Time) time.Time {
	if endsAt != nil {
		return *endsAt
	}
	return wakeNever
}

// PublishEvent wakes this process's live watches subscribed to topic (the
// full "<plugin key>:<topic>") and returns how many were woken. The payload
// becomes the first step's input; it is capped like a step output.
func PublishEvent(topic string, payload []byte) (int, error) {
	if err := checkTopic(topic); err != nil {
		return 0, err
	}
	r := running
	if r == nil {
		return 0, ErrNoWakeRunner
	}
	return r.wakeWatches(topic, capBytes(payload, stepOutputCap)), nil
}

// wakeWatches queues the event on every matching watch and makes it due
// now; the runner is poked so the run starts without waiting for a tick.
func (r *Runner) wakeWatches(topic string, payload []byte) int {
	ws := r.watches
	ws.mu.Lock()
	n := 0
	if ws.leased {
		now := time.Now()
		for _, e := range ws.entries {
			if e.gone || e.m.Paused || e.m.WakeOn == "" || !WakeMatches(e.m.WakeOn, topic) {
				continue
			}
			if len(e.wakeQ) >= wakeQueueMax {
				e.wakeQ = e.wakeQ[1:]
			}
			e.wakeQ = append(e.wakeQ, wakeEvent{topic: topic, payload: payload})
			if !e.running && e.wakeReturn.IsZero() {
				// The slot to go back to once the queue is drained.
				e.wakeReturn = e.next
				e.next = now
			}
			n++
		}
	}
	ws.mu.Unlock()
	if n > 0 {
		r.Wake()
	}
	return n
}
